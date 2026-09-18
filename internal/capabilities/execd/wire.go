package execd

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"

	"google.golang.org/protobuf/proto"
)

// ProtocolVersion is bumped on every wire change. The child is always
// the same binary as the parent, and the Hello handshake rejects a
// mismatch instead of running half-updated.
const ProtocolVersion = 2

// maxFrameBytes caps one frame in both directions.
const maxFrameBytes = 8 << 20

// Wire error codes carried by Response_Error.
const (
	CodeInternal         = "internal"
	CodeInvalid          = "invalid"
	CodeNotFound         = "not_found"
	CodeCanceled         = "canceled"
	CodeDeadlineExceeded = "deadline_exceeded"
	CodeNotBound         = "not_bound"
	CodeAlreadyBound     = "already_bound"
	CodeVersionMismatch  = "protocol_version_mismatch"
	CodeSequenceGap      = "sequence_gap"
	CodeMethodNotFound   = "method_not_found"
	CodeUnavailable      = "unavailable"
	CodeFrameTooLarge    = "frame_too_large"
)

// RPCError is the typed failure a Response_Error becomes on the client.
type RPCError struct {
	Method  string
	Code    string
	Message string
}

func (e *RPCError) Error() string {
	if e.Method == "" {
		return fmt.Sprintf("execd: %s: %s", e.Code, e.Message)
	}
	return fmt.Sprintf("execd: %s: %s: %s", e.Method, e.Code, e.Message)
}

// IsCode reports whether err carries the given wire error code.
func IsCode(err error, code string) bool {
	var rpc *RPCError
	return errors.As(err, &rpc) && rpc.Code == code
}

// IsNotFound reports the "unknown process" class, which callers treat
// as an idempotent no-op in release/terminate paths.
func IsNotFound(err error) bool { return IsCode(err, CodeNotFound) }

// IsCanceled reports a request the child canceled (client ctx cancel).
func IsCanceled(err error) bool {
	return IsCode(err, CodeCanceled) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded)
}

func errorResponse(code, format string, args ...any) *Response {
	return &Response{Body: &Response_Error{Error: &Error{
		Code:    code,
		Message: fmt.Sprintf(format, args...),
	}}}
}

func errorFromResponse(method string, e *Error) error {
	return &RPCError{Method: method, Code: e.GetCode(), Message: e.GetMessage()}
}

// deadlineMillis renders ctx's remaining budget as the relative
// deadline the wire carries. Zero means "no wire deadline".
func deadlineMillis(ctx context.Context) int64 {
	deadline, ok := ctx.Deadline()
	if !ok {
		return 0
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return 1
	}
	return remaining.Milliseconds()
}

// encodeFrame serializes one protobuf frame with its length prefix.
func encodeFrame(frame *Frame) ([]byte, error) {
	raw, err := proto.Marshal(frame)
	if err != nil {
		return nil, fmt.Errorf("execd: marshal frame: %w", err)
	}
	if len(raw) > maxFrameBytes {
		return nil, fmt.Errorf("execd: frame of %d bytes exceeds the %d byte cap",
			len(raw), maxFrameBytes)
	}
	out := make([]byte, 4+len(raw))
	binary.BigEndian.PutUint32(out[:4], uint32(len(raw)))
	copy(out[4:], raw)
	return out, nil
}

// decodeFrame reads one length-prefixed protobuf frame.
func decodeFrame(r *bufio.Reader) (*Frame, error) {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}
	size := binary.BigEndian.Uint32(header[:])
	if size == 0 || size > maxFrameBytes {
		return nil, fmt.Errorf("execd: frame size %d out of range", size)
	}
	raw := make([]byte, size)
	if _, err := io.ReadFull(r, raw); err != nil {
		return nil, err
	}
	frame := &Frame{}
	if err := proto.Unmarshal(raw, frame); err != nil {
		return nil, fmt.Errorf("execd: decode frame: %w", err)
	}
	return frame, nil
}
