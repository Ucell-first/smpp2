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
		commandLength:  16,
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
