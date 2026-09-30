// Package phone connects clients (browser tabs, app instances) to virtual phones and calls.
// It is the authorization boundary for everything a client does on the SIP side.
package phone

import (
	"time"

	"github.com/revocx35/web-ip-phone/internal/sipua"
)

// Frame types of binary WebSocket messages.
const (
	FrameAudio byte = 0x01 // followed by G.711 payload in the call's codec
)

// Outbound is the client connection as seen by the service (implemented by the WebSocket layer).
type Outbound interface {
	// SendJSON queues a control message; false if the connection is gone or congested.
	SendJSON(v any) bool
	// SendBinary queues an audio frame; frames are dropped when the connection is slow.
	SendBinary(b []byte) bool
	// Close ends the connection with a reason shown to the user.
	Close(reason string)
}

// Inbound messages from clients.
type Inbound struct {
	Type   string  `json:"type"`
	Req    string  `json:"req,omitempty"`
	Phones []int64 `json:"phones,omitempty"`
	Phone  int64   `json:"phone,omitempty"`
	Number string  `json:"number,omitempty"`
	Call   string  `json:"call,omitempty"`
	Digits string  `json:"digits,omitempty"`
	On     bool    `json:"on,omitempty"`
}

// PhoneView is a virtual phone as shown to a client.
type PhoneView struct {
	ID          int64          `json:"id"`
	Label       string         `json:"label"`
	SIPUser     string         `json:"sipUser"`
	DisplayName string         `json:"displayName"`
	PBXID       int64          `json:"pbxId"`
	PBXName     string         `json:"pbxName"`
	Owned       bool           `json:"owned"`
	Register    bool           `json:"register"`
	Reg         sipua.RegState `json:"reg"`
	Online      bool           `json:"online"` // this client receives calls on it
}

// CallView is a call as shown to a client.
type CallView struct {
	ID         string          `json:"id"`
	PhoneID    int64           `json:"phoneId"`
	Direction  string          `json:"direction"`
	Remote     string          `json:"remote"`
	RemoteName string          `json:"remoteName"`
	State      sipua.CallState `json:"state"`
	Codec      string          `json:"codec"`
	Hold       bool            `json:"hold"`
	RemoteHold bool            `json:"remoteHold"`
	StartedAt  time.Time       `json:"startedAt"`
	AnsweredAt time.Time       `json:"answeredAt,omitzero"`
	EndReason  string          `json:"endReason,omitempty"`
	EndStatus  string          `json:"endStatus,omitempty"`
	// Attached: this client carries the call's audio.
	Attached bool `json:"attached"`
	// Mine: the call belongs to this user (dialed or answered by them); false for
	// incoming calls that are only offered.
	Mine bool `json:"mine"`
}

type msgHello struct {
	Type       string      `json:"type"`
	User       any         `json:"user"`
	Phones     []PhoneView `json:"phones"`
	Calls      []CallView  `json:"calls"`
	ServerTime time.Time   `json:"serverTime"`
	Version    string      `json:"version"`
}

type msgPhones struct {
	Type   string      `json:"type"`
	Phones []PhoneView `json:"phones"`
}

type msgCall struct {
	Type string   `json:"type"`
	Call CallView `json:"call"`
}

type msgError struct {
	Type  string `json:"type"`
	Req   string `json:"req,omitempty"`
	Code  string `json:"code"`
	Error string `json:"error"`
}

type msgAck struct {
	Type string `json:"type"`
	Req  string `json:"req,omitempty"`
	Call string `json:"call,omitempty"`
}

type msgDTMF struct {
	Type  string `json:"type"`
	Call  string `json:"call"`
	Digit string `json:"digit"`
}
