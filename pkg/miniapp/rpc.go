// SPDX-License-Identifier: Unlicense OR MIT

package miniapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// rpc is a connection to a browser's automation endpoint: the DevTools
// protocol of a Chromium-based browser or WebDriver BiDi of Firefox. Both
// send JSON commands with an id, answer each with the same id, and push
// events with a method; they differ in how an answer says it failed.
type rpc struct {
	conn *websocket.Conn
	bidi bool
	// onEvent is called on the reading goroutine for every event.
	onEvent func(method string, params json.RawMessage)

	mu      sync.Mutex
	nextID  int
	replies map[int]chan rpcReply
	err     error
}

type rpcReply struct {
	result json.RawMessage
	err    error
}

func dialRPC(ctx context.Context, url string, bidi bool, onEvent func(string, json.RawMessage)) (*rpc, error) {
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(8 << 20)
	return &rpc{conn: conn, bidi: bidi, onEvent: onEvent, replies: map[int]chan rpcReply{}}, nil
}

// read dispatches answers and events until the connection ends, and
// returns why it ended.
func (r *rpc) read(ctx context.Context) error {
	for {
		_, data, err := r.conn.Read(ctx)
		if err != nil {
			r.mu.Lock()
			r.err = err
			for id, reply := range r.replies {
				reply <- rpcReply{err: err}
				delete(r.replies, id)
			}
			r.mu.Unlock()
			return err
		}
		var message struct {
			ID     *int            `json:"id"`
			Type   string          `json:"type"`
			Result json.RawMessage `json:"result"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			// The DevTools protocol's error is an object, BiDi's a code
			// beside a message.
			Error   json.RawMessage `json:"error"`
			Message string          `json:"message"`
		}
		if json.Unmarshal(data, &message) != nil {
			continue
		}
		if message.ID == nil {
			if message.Method != "" && r.onEvent != nil {
				r.onEvent(message.Method, message.Params)
			}
			continue
		}
		r.mu.Lock()
		reply := r.replies[*message.ID]
		delete(r.replies, *message.ID)
		r.mu.Unlock()
		if reply == nil {
			continue
		}
		var answer rpcReply
		switch {
		case r.bidi && message.Type == "error":
			var code string
			_ = json.Unmarshal(message.Error, &code)
			answer.err = fmt.Errorf("%s: %s", code, message.Message)
		case !r.bidi && len(message.Error) > 0:
			var cdpError struct {
				Message string `json:"message"`
			}
			_ = json.Unmarshal(message.Error, &cdpError)
			answer.err = errors.New(cdpError.Message)
		default:
			answer.result = message.Result
		}
		reply <- answer
	}
}

// call sends a command and waits for its answer.
func (r *rpc) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	r.mu.Lock()
	if r.err != nil {
		err := r.err
		r.mu.Unlock()
		return nil, err
	}
	r.nextID++
	id := r.nextID
	reply := make(chan rpcReply, 1)
	r.replies[id] = reply
	r.mu.Unlock()

	request := map[string]any{"id": id, "method": method}
	if params != nil {
		request["params"] = params
	} else if r.bidi {
		// BiDi wants params on every command.
		request["params"] = map[string]any{}
	}
	payload, err := json.Marshal(request)
	if err == nil {
		err = r.conn.Write(ctx, websocket.MessageText, payload)
	}
	if err != nil {
		r.forget(id)
		return nil, err
	}
	select {
	case answer := <-reply:
		if answer.err != nil {
			return nil, fmt.Errorf("%s: %w", method, answer.err)
		}
		return answer.result, nil
	case <-ctx.Done():
		r.forget(id)
		return nil, ctx.Err()
	case <-time.After(10 * time.Second):
		r.forget(id)
		return nil, fmt.Errorf("%s timed out", method)
	}
}

// send sends a command without waiting for its answer.
func (r *rpc) send(ctx context.Context, method string, params any) error {
	r.mu.Lock()
	r.nextID++
	id := r.nextID
	r.mu.Unlock()
	if params == nil {
		params = map[string]any{}
	}
	payload, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		return err
	}
	return r.conn.Write(ctx, websocket.MessageText, payload)
}

func (r *rpc) forget(id int) {
	r.mu.Lock()
	delete(r.replies, id)
	r.mu.Unlock()
}

func (r *rpc) close() {
	_ = r.conn.Close(websocket.StatusNormalClosure, "")
}
