package smpp

import (
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

type connection struct {
	host           string
	port           int
	conn           net.Conn
	connectTimeout time.Duration
	readTimeout    time.Duration
}

func newConnection(host string, port int, connectTimeout, readTimeout time.Duration) *connection {
	return &connection{
		host:           host,
		port:           port,
		connectTimeout: connectTimeout,
		readTimeout:    readTimeout,
	}
}

func (c *connection) connect() error {
	addr := fmt.Sprintf("%s:%d", c.host, c.port)
	dialer := net.Dialer{Timeout: c.connectTimeout}

	conn, err := dialer.Dial("tcp", addr)
	if err != nil {
		return err
	}

	c.conn = conn
	return nil
}

func (c *connection) connectTLS(config *tls.Config) error {
	addr := fmt.Sprintf("%s:%d", c.host, c.port)
	dialer := net.Dialer{Timeout: c.connectTimeout}

	if config == nil {
		config = &tls.Config{InsecureSkipVerify: true}
	}

	conn, err := tls.DialWithDialer(&dialer, "tcp", addr, config)
	if err != nil {
		return err
	}

	c.conn = conn
	return nil
}

func (c *connection) close() error {
	if c.conn == nil {
		return nil
	}

	err := c.conn.Close()
	c.conn = nil
	return err
}

func (c *connection) writePDU(p *pdu) error {
	if c.conn == nil {
		return errors.New("not connected")
	}

	// Set deadline for write
	err := c.conn.SetWriteDeadline(time.Now().Add(c.readTimeout))
	if err != nil {
		return err
	}

	// Calculate PDU length
	p.commandLength = uint32(16 + len(p.body))

	// Write PDU header
	headerBuf := make([]byte, 16)
	binary.BigEndian.PutUint32(headerBuf[0:4], p.commandLength)
	binary.BigEndian.PutUint32(headerBuf[4:8], p.commandID)
	binary.BigEndian.PutUint32(headerBuf[8:12], p.commandStatus)
	binary.BigEndian.PutUint32(headerBuf[12:16], p.sequenceNumber)

	_, err = c.conn.Write(headerBuf)
	if err != nil {
		return err
	}

	if len(p.body) > 0 {
		_, err = c.conn.Write(p.body)
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *connection) readPDU() (*pdu, error) {
	if c.conn == nil {
		return nil, errors.New("not connected")
	}

	err := c.conn.SetReadDeadline(time.Now().Add(c.readTimeout))
	if err != nil {
		return nil, err
	}

	headerBuf := make([]byte, 16)
	_, err = io.ReadFull(c.conn, headerBuf)
	if err != nil {
		return nil, err
	}

	p := &pdu{}
	p.commandLength = binary.BigEndian.Uint32(headerBuf[0:4])
	p.commandID = binary.BigEndian.Uint32(headerBuf[4:8])
	p.commandStatus = binary.BigEndian.Uint32(headerBuf[8:12])
	p.sequenceNumber = binary.BigEndian.Uint32(headerBuf[12:16])

	// Read PDU body
	bodyLength := p.commandLength - 16
	if bodyLength > 0 {
		p.body = make([]byte, bodyLength)
		_, err = io.ReadFull(c.conn, p.body)
		if err != nil {
			return nil, err
		}
	} else {
		p.body = []byte{}
	}

	return p, nil
}
