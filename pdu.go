// package/smpp/pdu.go
package smpp

// pdu represents an SMPP Protocol Data Unit
type pdu struct {
	commandLength  uint32
	commandID      uint32
	commandStatus  uint32
	sequenceNumber uint32
	body           []byte
}

// newPDU creates a new PDU
func newPDU(commandID, sequenceNumber uint32) *pdu {
	return &pdu{
		commandLength:  16, // Just header initially
		commandID:      commandID,
		commandStatus:  0,
		sequenceNumber: sequenceNumber,
		body:           make([]byte, 0),
	}
}

// write appends raw bytes to the PDU body
func (p *pdu) write(data []byte) {
	p.body = append(p.body, data...)
}

// writeByte appends a byte to the PDU body
func (p *pdu) writeByte(b byte) {
	p.body = append(p.body, b)
}

// writeString writes a C-style string (null-terminated) to the PDU body
func (p *pdu) writeString(s string) {
	if len(s) > 0 {
		p.body = append(p.body, []byte(s)...)
	}
	// Add null terminator
	p.body = append(p.body, 0)
}

// writeTLV writes a Tag-Length-Value parameter to the PDU body
func (p *pdu) writeTLV(tag uint16, value []byte) {
	p.body = append(p.body, byte(tag>>8), byte(tag&0xFF))
	length := uint16(len(value))
	p.body = append(p.body, byte(length>>8), byte(length&0xFF))
	p.body = append(p.body, value...)
}

// getBytes returns the PDU body bytes from start to end
func (p *pdu) getBytes(start, end int) []byte {
	if start >= len(p.body) || end > len(p.body) || start >= end {
		return []byte{}
	}
	return p.body[start:end]
}
