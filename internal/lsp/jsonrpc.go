package lsp

// JSON-RPC 2.0 over the Language Server Protocol's own framing.
//
// LSP puts a header block in front of each JSON value:
//
//	Content-Length: 137\r\n
//	\r\n
//	{"jsonrpc":"2.0", ...}
//
// The length counts BYTES of the payload, not characters. Reading it as
// characters truncates every message containing a non-ASCII identifier, which
// this language is full of -- "残照花魄" is four runes and twelve bytes.
//
// A message may carry either method+params (a request or notification) or
// id+result/error (a response). One struct covers all four shapes because the
// decoder only fills the fields that are present.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

// rpcError is the error member of a failed response.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("jsonrpc %d: %s", e.Code, e.Message) }

// Standard JSON-RPC error codes this server uses.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInternalError  = -32603
)

// msg is one JSON-RPC message of any shape.
type msg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// isCall reports whether a message expects a response. A notification carries
// no id, and JSON-RPC forbids answering one.
func (m *msg) isCall() bool { return len(m.ID) > 0 }

// conn reads and writes framed messages.
//
// Writes take a mutex because a request handler produces a response while the
// read loop may also be writing a server-initiated notification. Interleaved
// writes would corrupt the frame.
// conn reads and writes framed messages.
//
// Writes go through an unbounded queue drained by a goroutine, never straight
// to the writer. That is not an optimisation: writing inline in the read loop
// deadlocks the moment the peer is also writing. A client that sends a
// notification and then a request -- which is every client, on every keystroke
// -- blocks on the request while this side blocks publishing diagnostics for
// the notification, and neither ever reads. Measured: hover hung for the full
// test timeout with one goroutine parked in pipe.write on each side.
type conn struct {
	r *bufio.Reader
	w io.Writer

	mu     sync.Mutex
	queue  [][]byte
	wake   chan struct{}
	closed bool
}

func newConn(r io.Reader, w io.Writer) *conn {
	c := &conn{r: bufio.NewReader(r), w: w, wake: make(chan struct{}, 1)}
	go c.drain()
	return c
}

// drain writes queued frames in order, for as long as the connection is open.
func (c *conn) drain() {
	for {
		c.mu.Lock()
		if len(c.queue) == 0 {
			if c.closed {
				c.mu.Unlock()
				return
			}
			c.mu.Unlock()
			<-c.wake
			continue
		}
		batch := c.queue
		c.queue = nil
		c.mu.Unlock()
		for _, body := range batch {
			if _, err := fmt.Fprintf(c.w, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
				return
			}
			if _, err := c.w.Write(body); err != nil {
				return
			}
		}
	}
}

// Close stops the writer after flushing what is already queued.
func (c *conn) Close() {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// read returns the next message, or io.EOF when the client closes the pipe.
func (c *conn) read() (*msg, error) {
	length := -1
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			// End of the header block.
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			// A header this server does not understand. Skipping is right:
			// Content-Type is optional and its absence means utf-8.
			continue
		}
		if strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || n < 0 {
				return nil, fmt.Errorf("bad Content-Length %q", value)
			}
			length = n
		}
	}
	if length < 0 {
		return nil, errors.New("message without Content-Length")
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(c.r, buf); err != nil {
		return nil, err
	}
	var m msg
	if err := json.Unmarshal(buf, &m); err != nil {
		return nil, fmt.Errorf("bad JSON payload: %w", err)
	}
	return &m, nil
}

// write queues one message. It never blocks on the peer.
func (c *conn) write(v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return io.ErrClosedPipe
	}
	c.queue = append(c.queue, body)
	c.mu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default:
	}
	return nil
}

// reply sends the result of a call.
func (c *conn) reply(id json.RawMessage, result any) error {
	return c.write(msg{JSONRPC: "2.0", ID: id, Result: mustJSON(result)})
}

// replyErr sends a failure.
func (c *conn) replyErr(id json.RawMessage, code int, text string) error {
	return c.write(msg{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: text}})
}

// notify sends a server-initiated notification.
func (c *conn) notify(method string, params any) error {
	return c.write(msg{JSONRPC: "2.0", Method: method, Params: mustJSON(params)})
}

// mustJSON marshals a value known to be encodable.
//
// The alternatives are returning an error from every reply site or panicking.
// A nil result is legal JSON-RPC for a successful call, so an unencodable
// value here means the server built the wrong struct, which is a bug to
// surface at the earliest moment rather than a condition to handle.
func mustJSON(v any) json.RawMessage {
	if v == nil {
		return json.RawMessage("null")
	}
	b, err := json.Marshal(v)
	if err != nil {
		panic("lsp: cannot encode " + fmt.Sprintf("%T", v) + ": " + err.Error())
	}
	return b
}
