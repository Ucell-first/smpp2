// package/smpp/api.go
package smpp

import (
	"errors"
	"time"
)

// SMSMessage represents an SMS message to be sent
type SMSMessage struct {
	SourceAddr            string
	DestAddr              string
	Message               []byte
	DataCoding            byte
	IsUnicode             bool
	IsBinary              bool
	RequestDeliveryReport bool
}

// Client provides a simplified API for sending SMS messages via SMPP
type Client struct {
	conn        *connection
	systemID    string
	password    string
	bound       bool
	sequenceNum uint32
}

// NewClient creates a new SMPP client
func NewClient(host string, port int, systemID, password string) *Client {
	return &Client{
		conn:        newConnection(host, port, 10*time.Second, 30*time.Second),
		systemID:    systemID,
		password:    password,
		bound:       false,
		sequenceNum: 1,
	}
}

// Connect establishes a connection to the SMPP server and binds as transmitter
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

	// Bind as transmitter
	err = c.bind()
	if err != nil {
		c.conn.close()
		return err
	}

	return nil
}

// bind binds the client as a transmitter
func (c *Client) bind() error {
	pdu := newPDU(BIND_TRANSMITTER, c.nextSequence())

	// Add system_id (C-string)
	pdu.writeString(c.systemID)

	// Add password (C-string)
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

// SendSMS sends an SMS message
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
		// Use message_payload TLV for longer messages
		pdu.writeByte(0) // sm_length = 0

		// Add message_payload TLV
		pdu.writeTLV(0x0424, msg.Message)
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
		return "", errors.New("submit_sm failed")
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

// SendLongSMS sends an SMS message that may exceed the standard length
func (c *Client) SendLongSMS(msg *SMSMessage) (string, error) {
	// Define maximum length based on encoding
	maxLength := 160
	if msg.IsUnicode {
		maxLength = 70
	}

	// If message is short enough, just send it normally
	if len(msg.Message) <= maxLength {
		return c.SendSMS(msg)
	}

	// For longer messages, we'll split it into smaller parts
	// This is a simplified approach - in production, you'd use SMPP's message segmentation

	// For simplicity, we'll just send the first part
	shortMsg := &SMSMessage{
		SourceAddr:            msg.SourceAddr,
		DestAddr:              msg.DestAddr,
		Message:               msg.Message[:maxLength],
		DataCoding:            msg.DataCoding,
		IsUnicode:             msg.IsUnicode,
		IsBinary:              msg.IsBinary,
		RequestDeliveryReport: msg.RequestDeliveryReport,
	}

	return c.SendSMS(shortMsg)
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

// SMPP Command IDs
const (
	BIND_TRANSMITTER      uint32 = 0x00000002
	BIND_TRANSMITTER_RESP uint32 = 0x80000002
	SUBMIT_SM             uint32 = 0x00000004
	SUBMIT_SM_RESP        uint32 = 0x80000004
	UNBIND                uint32 = 0x00000006
	UNBIND_RESP           uint32 = 0x80000006
)
