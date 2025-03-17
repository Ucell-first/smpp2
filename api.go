package smpp

import (
	"errors"
	"fmt"
	"time"
)

type SMSMessage struct {
	SourceAddr            string
	DestAddr              string
	Message               []byte
	DataCoding            byte
	IsUnicode             bool
	IsBinary              bool
	RequestDeliveryReport bool
}

type Client struct {
	conn        *connection
	systemID    string
	password    string
	bound       bool
	sequenceNum uint32
}

func NewClient(host string, port int, systemID, password string) *Client {
	return &Client{
		conn:        newConnection(host, port, 10*time.Second, 30*time.Second),
		systemID:    systemID,
		password:    password,
		bound:       false,
		sequenceNum: 1,
	}
}

func (c *Client) Connect(useTLS bool) error {
	var err error

	if useTLS {
		err = c.conn.connectTLS(nil)
	} else {
		err = c.conn.connect()
	}

	if err != nil {
		return err
	}

	err = c.bind()
	if err != nil {
		c.conn.close()
		return err
	}

	return nil
}

func (c *Client) bind() error {
	pdu := newPDU(BIND_TRANSMITTER, c.nextSequence())
	pdu.writeString(c.systemID)
	pdu.writeString(c.password)

	// Add system_type (C-string), interface version, addr_ton, addr_npi, address_range
	pdu.writeString("") // system_type
	pdu.writeByte(0x34) // interface version (3.4)
	pdu.writeByte(0)    // addr_ton
	pdu.writeByte(0)    // addr_npi
	pdu.writeString("") // address_range

	resp, err := c.sendPDU(pdu)
	if err != nil {
		return err
	}

	if resp.commandStatus != 0 {
		return errors.New("bind failed")
	}

	c.bound = true
	return nil
}

func (c *Client) SendSMS(msg *SMSMessage) (string, error) {
	if !c.bound {
		return "", errors.New("not bound to SMPP server")
	}

	// Set data coding based on content type
	dataCoding := byte(0) // Default GSM
	if msg.IsUnicode {
		dataCoding = 0x08 // UCS2
	} else if msg.IsBinary {
		dataCoding = 0x04 // Binary
	}

	// Create PDU
	pdu := newPDU(SUBMIT_SM, c.nextSequence())

	// Add mandatory parameters
	pdu.writeString("") // service_type
	pdu.writeByte(0)    // source_addr_ton
	pdu.writeByte(0)    // source_addr_npi
	pdu.writeString(msg.SourceAddr)
	pdu.writeByte(1) // dest_addr_ton
	pdu.writeByte(1) // dest_addr_npi
	pdu.writeString(msg.DestAddr)

	esmClass := byte(0)
	if msg.IsBinary {
		esmClass |= 0x04 // Set binary flag
	}
	pdu.writeByte(esmClass) // esm_class
	pdu.writeByte(0)        // protocol_id
	pdu.writeByte(0)        // priority_flag
	pdu.writeString("")     // schedule_delivery_time
	pdu.writeString("")     // validity_period

	// Set registered delivery if delivery report requested
	regDelivery := byte(0)
	if msg.RequestDeliveryReport {
		regDelivery = 1
	}
	pdu.writeByte(regDelivery) // registered_delivery
	pdu.writeByte(0)           // replace_if_present_flag
	pdu.writeByte(dataCoding)  // data_coding
	pdu.writeByte(0)           // sm_default_msg_id

	// Handle message length
	if len(msg.Message) > 254 {
		// Message too long, return an error
		return "", fmt.Errorf("message too long (%d bytes), max is 254 bytes", len(msg.Message))
	} else {
		pdu.writeByte(byte(len(msg.Message))) // sm_length
		pdu.write(msg.Message)                // short_message
	}

	// Send the PDU
	resp, err := c.sendPDU(pdu)
	if err != nil {
		return "", err
	}

	if resp.commandStatus != 0 {
		// Map some common SMPP error codes
		errorMessage := "unknown error"
		switch resp.commandStatus {
		case 1:
			errorMessage = "message ID invalid"
		case 2:
			errorMessage = "bind failed"
		case 3:
			errorMessage = "invalid priority flag"
		case 4:
			errorMessage = "invalid registered delivery flag"
		case 5:
			errorMessage = "system error"
		case 6:
			errorMessage = "invalid source address"
		case 7:
			errorMessage = "invalid destination address"
		case 8:
			errorMessage = "invalid message ID"
		case 10:
			errorMessage = "invalid message"
		case 88:
			errorMessage = "throttled - rate limit exceeded"
		}
		return "", fmt.Errorf("submit_sm failed with status: %d (%s)", resp.commandStatus, errorMessage)
	}

	// Extract message ID from response
	messageID := ""
	if len(resp.body) > 0 {
		// Remove null terminator if present
		if resp.body[len(resp.body)-1] == 0 {
			messageID = string(resp.body[:len(resp.body)-1])
		} else {
			messageID = string(resp.body)
		}
	}

	return messageID, nil
}

func (c *Client) SendLongSMS(msg *SMSMessage) (string, error) {
	// Define maximum length based on encoding
	maxLength := 153 // For segmented GSM messages, we use 153 chars instead of 160
	if msg.IsUnicode {
		maxLength = 67 // For segmented Unicode messages, we use 67 chars instead of 70
	}

	// If message is short enough, just send it normally
	if len(msg.Message) <= maxLength {
		return c.SendSMS(msg)
	}

	// For longer messages, we need proper segmentation
	messageLen := len(msg.Message)
	partCount := (messageLen + maxLength - 1) / maxLength // Ceiling division

	// We'll only return the ID of the first message part
	var firstMessageID string

	// Split message into parts and send each part
	for i := 0; i < partCount; i++ {
		// Calculate the start and end indices for this part
		start := i * maxLength
		end := start + maxLength
		if end > messageLen {
			end = messageLen
		}

		// Create message part
		partMsg := &SMSMessage{
			SourceAddr:            msg.SourceAddr,
			DestAddr:              msg.DestAddr,
			Message:               msg.Message[start:end],
			DataCoding:            msg.DataCoding,
			IsUnicode:             msg.IsUnicode,
			IsBinary:              msg.IsBinary,
			RequestDeliveryReport: msg.RequestDeliveryReport,
		}

		// Send message part
		messageID, err := c.SendSMS(partMsg)
		if err != nil {
			return "", fmt.Errorf("failed to send part %d/%d: %w", i+1, partCount, err)
		}

		// Store the ID of the first message part
		if i == 0 {
			firstMessageID = messageID
		}

		// Add a delay between message parts to avoid throttling
		if i < partCount-1 {
			time.Sleep(200 * time.Millisecond)
		}
	}

	return firstMessageID, nil
}

// Disconnect closes the connection to the SMPP server
func (c *Client) Disconnect() error {
	if c.bound {
		// Send unbind command
		pdu := newPDU(UNBIND, c.nextSequence())
		_, err := c.sendPDU(pdu)
		if err != nil {
			c.conn.close()
			return err
		}
		c.bound = false
	}

	return c.conn.close()
}

// nextSequence returns the next sequence number for PDUs
func (c *Client) nextSequence() uint32 {
	seq := c.sequenceNum
	c.sequenceNum++
	if c.sequenceNum > 0x7FFFFFFF {
		c.sequenceNum = 1
	}
	return seq
}

// sendPDU sends a PDU and waits for the response
func (c *Client) sendPDU(pdu *pdu) (*pdu, error) {
	err := c.conn.writePDU(pdu)
	if err != nil {
		return nil, err
	}

	// Read response
	return c.conn.readPDU()
}

const (
	BIND_TRANSMITTER      uint32 = 0x00000002
	BIND_TRANSMITTER_RESP uint32 = 0x80000002
	SUBMIT_SM             uint32 = 0x00000004
	SUBMIT_SM_RESP        uint32 = 0x80000004
	UNBIND                uint32 = 0x00000006
	UNBIND_RESP           uint32 = 0x80000006
)
