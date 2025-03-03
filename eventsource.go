package eventsource

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/nsf/eventsource/buffer"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// This error is delivered via callback when HTTP response contains a non-200 status. Use errors.Is to check for this error.
var ErrInvalidStatus = errors.New("eventsource: http response status code is not 200")

// This error is delivered via callback when HTTP response contains Content-Type set to something else than "text/event-stream". Use errors.Is to check for this error.
var ErrInvalidContentType = errors.New("eventsource: http response content type is not text/event-stream")

// This error is delivered via callback when there was not enough space in the buffer while processing the message. Use errors.Is to check for this error.
var ErrBufferFull = buffer.ErrBufferFull

// Default maximum size of "id" field buffer.
const DefaultMaxID = 256

// Default maximum size of "event" field buffer.
const DefaultMaxEvent = 256

// Default maximum size of "data" field buffer.
const DefaultMaxData = 4 * 1024 * 1024

type Message struct {
	// ID of the message. Corresponds to "id" field in SSE protocol. Nil if event did not provide the field.
	// When valid, this slice points to internal temporary buffer and might become invalid when callback returns.
	ID []byte

	// Type of the message. Corresponds to "event" field in SSE protocol. Nil if event did not provide the field.
	// When valid, this slice points to internal temporary buffer and might become invalid when callback returns.
	Event []byte

	// Data of the message. Corresponds to "data" field in SSE protocol. Nil if event did not provide the field.
	// When valid, this slice points to internal temporary buffer and becomes invalid when callback returns.
	// Copy the slice if you intend to handle the data later (and in many cases you want to do that to avoid
	// stalling EventSource processing goroutine).
	Data []byte
}

var (
	knownFieldNameID    = []byte("id")
	knownFieldNameEvent = []byte("event")
	knownFieldNameData  = []byte("data")
	knownFieldNameRetry = []byte("retry")
)

// Messages are delivered via this callback. See: WithCallback.
type Callback func(msg Message, err error)

// EventSource
type EventSource struct {
	url          string
	ctx          context.Context
	cancel       func()
	client       *http.Client
	req          *http.Request
	callback     Callback
	bp           BufferParameters
	wg           sync.WaitGroup
	idBuf        []byte
	eventBuf     []byte
	dataBuf      []byte
	msgErr       error
	retryTimeout time.Duration
}

func growMaybeLimit(s []byte, reqCap, limit int) []byte {
	if cap(s) < reqCap {
		// grow the buffer, manually, we want to keep it below limit in capacity as well
		newCap := min(limit, max(reqCap, cap(s)*2))
		ss := make([]byte, len(s), newCap)
		copy(ss, s)
		return ss
	}
	return s
}

func appendLimit(s []byte, ns []byte, limit int) ([]byte, error) {
	if len(s) != 0 {
		nlen := len(s) + 1 + len(ns)
		if nlen > limit {
			return nil, ErrBufferFull
		}
		s = growMaybeLimit(s, nlen, limit)
		s = append(s, '\n')
		s = append(s, ns...)
		return s, nil
	} else {
		nlen := len(ns)
		if nlen > limit {
			return nil, ErrBufferFull
		}
		s = growMaybeLimit(s, nlen, limit)
		s = append(s, ns...)
		return s, nil
	}
}

func (es *EventSource) dispatch(msg Message, err error) {
	if es.callback != nil {
		es.callback(msg, err)
	}
}

// Functions that return "Option" can be used with New when creating an EventSource.
type Option func(es *EventSource)

// For every parsed Event we hold its parts in-memory. This includes ID, Event and Data.
// This structure defines size limits for these parts. If set to 0, the default size is used.
// If MaxReadBuffer is 0, it is set to max(MaxID, MaxEvent, MaxData)+len("event: \r\n")+1.
//
// The default sizes are:
//
//   - ID - 256 bytes
//   - Event - 256 bytes
//   - Data - 4*1024*1024 bytes
//
// How it works:
//   - EventSource reads data from http response body to read buffer.
//   - Once a complete field is recognized, the field value is copied into its own temporary buffer (id, event or data).
//   - All buffers grow as needed, but up to their max value.
//   - Once end of message is reached, the Event structure is assembled. It contains references to temporary buffers.
//   - The Event is dispatched to the callback.
//   - After that the process is repeated, buffers are reused.
type BufferParameters struct {
	MaxID         int
	MaxEvent      int
	MaxData       int
	MaxReadBuffer int
}

// Overrides HTTP client used for making HTTP requests. The default is http.DefaultClient.
func WithClient(cli *http.Client) Option {
	return func(es *EventSource) {
		es.client = cli
	}
}

// Overrides the request prototype that is used for making HTTP requests.
func WithRequest(req *http.Request) Option {
	return func(es *EventSource) {
		es.req = req
	}
}

func WithURL(url string) Option {
	return func(es *EventSource) {
		es.url = url
	}
}

func WithCallback(callback Callback) Option {
	return func(es *EventSource) {
		es.callback = callback
	}
}

func WithContext(ctx context.Context) Option {
	return func(es *EventSource) {
		es.ctx = ctx
	}
}

func WithBufferParameters(bp BufferParameters) Option {
	return func(es *EventSource) {
		es.bp = bp
	}
}

func splitLine(line []byte) ([]byte, []byte) {
	i := bytes.Index(line, []byte(`:`))
	if i == -1 {
		return line, nil
	}
	key := line[:i]
	value := line[i+1:]
	if i+1 < len(line) && line[i+1] == ' ' {
		value = line[i+2:]
	}
	return key, value
}

func (es *EventSource) perMessageReset() {
	es.idBuf = es.idBuf[:0]
	es.eventBuf = es.eventBuf[:0]
	es.dataBuf = es.dataBuf[:0]
	es.msgErr = nil
}

func (es *EventSource) perRequestReset() {
	es.idBuf = nil
	es.eventBuf = nil
	es.dataBuf = nil
	es.msgErr = nil
}

func (es *EventSource) retryTimeoutSleep() {
	time.Sleep(es.retryTimeout)
}

func (es *EventSource) processRequest() bool {
	defer es.retryTimeoutSleep()
	req := es.req.Clone(es.ctx)
	if len(es.idBuf) != 0 {
		req.Header.Set("Last-Event-Id", string(es.idBuf))
	}
	resp, err := es.client.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			// not an unexpected error
			return false
		}
		es.dispatch(Message{}, fmt.Errorf("eventsource: http response error: %w", err))
		return true
	}
	defer io.Copy(io.Discard, resp.Body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		es.dispatch(Message{}, ErrInvalidStatus)
		return true
	}
	if resp.Header.Get("Content-Type") != "text/event-stream" {
		es.dispatch(Message{}, ErrInvalidContentType)
		return true
	}

	// One might ask: "Why not reuse the buffers?". I think it's ok in this case to reset it on a per-request
	// basis. The goal for Server-Sent Events is to serve lots of events through a single established
	// connection. Thus connection shouldn't be normally broken. And if it happens, let's reset the buffers.
	// Letting the GC do its job.
	es.perRequestReset()
	rb := buffer.New(resp.Body, es.bp.MaxReadBuffer)
	for {
		line, err := rb.ReadLine()
		if err != nil {
			if errors.Is(err, context.Canceled) {
				// not an unexpected error, signal we want to stop
				return false
			} else if errors.Is(err, io.EOF) {
				// this could happen, but it means we should silently retry the request
				return true
			} else {
				// otherwise report the error and retry the request
				es.dispatch(Message{}, fmt.Errorf("eventsource: http response body read error: %w", err))
				return true
			}
		}
		if len(line) == 0 {
			// empty line aka "\n\n", it separates events within a stream and acts as a "submit" signal
			if es.msgErr == nil {
				es.dispatch(Message{ID: es.idBuf, Event: es.eventBuf, Data: es.dataBuf}, nil)
			} else {
				es.dispatch(Message{}, es.msgErr)
			}
			es.perMessageReset()
			continue
		}
		if es.msgErr != nil {
			// when message error was set, we're skipping all other lines waiting for "submit" signal
			continue
		}
		key, val := splitLine(line)
		if len(key) == 0 {
			// comment, skip it
			continue
		}
		// handle all known fields
		if bytes.Equal(key, knownFieldNameID) {
			es.idBuf = es.idBuf[:0]
			es.idBuf, err = appendLimit(es.idBuf, val, es.bp.MaxID)
			if err != nil {
				es.msgErr = fmt.Errorf("eventsource: id field is too long: %w", err)
			}
		} else if bytes.Equal(key, knownFieldNameEvent) {
			es.eventBuf = es.eventBuf[:0]
			es.eventBuf, err = appendLimit(es.eventBuf, val, es.bp.MaxEvent)
			if err != nil {
				es.msgErr = fmt.Errorf("eventsource: event field is too long: %w", err)
			}
		} else if bytes.Equal(key, knownFieldNameData) {
			es.dataBuf, err = appendLimit(es.dataBuf, val, es.bp.MaxData)
			if err != nil {
				es.msgErr = fmt.Errorf("eventsource: data field is too long: %w", err)
			}
		} else if bytes.Equal(key, knownFieldNameRetry) {
			ms, err := strconv.ParseInt(string(val), 10, 64)
			if err == nil {
				es.retryTimeout = time.Duration(ms) * time.Millisecond
			}
		}
	}
}

func New(options ...Option) (*EventSource, error) {
	es := &EventSource{retryTimeout: 1 * time.Second}
	es.wg.Add(1)
	for _, opt := range options {
		opt(es)
	}
	if es.req == nil {
		var err error
		es.req, err = http.NewRequest("GET", es.url, nil)
		if err != nil {
			return nil, err
		}
	}
	if es.client == nil {
		es.client = http.DefaultClient
	}
	if es.ctx == nil {
		es.ctx = context.Background()
	}
	if es.bp.MaxID == 0 {
		es.bp.MaxID = DefaultMaxID
	}
	if es.bp.MaxEvent == 0 {
		es.bp.MaxEvent = DefaultMaxEvent
	}
	if es.bp.MaxData == 0 {
		es.bp.MaxData = DefaultMaxData
	}
	if es.bp.MaxReadBuffer == 0 {
		es.bp.MaxReadBuffer = max(es.bp.MaxID, es.bp.MaxEvent, es.bp.MaxData) + len("event: \r\n") + 1
	}
	es.ctx, es.cancel = context.WithCancel(es.ctx)
	go func() {
		for es.processRequest() {
		}
		es.wg.Done()
	}()
	return es, nil
}

// Close forcefully and gracefully stops EventSource from receiving messages. It waits until internal goroutine returns.
// Once Close() returns it's guaranteed that no callback calls will be made. After calling Close() the EventSource
// cannot be reused and is left for garbage collection.
func (es *EventSource) Close() {
	es.cancel()
	es.wg.Wait()
}
