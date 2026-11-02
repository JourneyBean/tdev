package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const Version = 1

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
	Exit    int    `json:"-"`
}

func (e *Error) Error() string { return e.Message }
func Fail(code string, exit int, format string, args ...any) error {
	return &Error{Code: code, Exit: exit, Message: fmt.Sprintf(format, args...)}
}
func Normalize(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return &Error{Code: "RUNTIME_ERROR", Exit: 1, Message: err.Error()}
}

type Response struct {
	ProtocolVersion int    `json:"protocol_version"`
	OK              bool   `json:"ok"`
	Data            any    `json:"data,omitempty"`
	Error           *Error `json:"error,omitempty"`
}

func Write(w io.Writer, data any, err error) error {
	r := Response{ProtocolVersion: Version, OK: err == nil, Data: data}
	if err != nil {
		r.Data = nil
		r.Error = Normalize(err)
	}
	return json.NewEncoder(w).Encode(r)
}
