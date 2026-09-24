// Copyright 2026 Versity Software
// This file is licensed under the Apache License, Version 2.0
// (the "License"); you may not use this file except in compliance
// with the License.  You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package controllers

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/fil-forge/versitygw/debuglogger"
	"github.com/fil-forge/versitygw/s3err"
	"github.com/fil-forge/versitygw/s3log"
	"github.com/gofiber/fiber/v3"
	"github.com/valyala/fasthttp"
)

// A pending response keeps a slow request's connection alive the way S3
// does for CompleteMultipartUpload. Clients abort a request after a read
// timeout of silence (60 s for the AWS CLI), so once the outcome is late the
// response commits to 200 OK and sends whitespace until the outcome
// arrives. The body then ends with the result document, or with an <Error>
// document in its place, which AWS SDKs rewrite to a 500 and retry.

// pendingResponse is a controller outcome that arrives after the controller
// returned.
type pendingResponse struct {
	// interval paces the whitespace sent while the outcome is pending.
	interval time.Duration
	// outcome delivers the final response exactly once.
	outcome <-chan pendingOutcome
}

// pendingOutcome is what a controller would have returned. response is
// never nil.
type pendingOutcome struct {
	response *Response
	err      error
}

// streamPending sends a pending response as a 200 whose body S3 clients
// read as the eventual result: the XML declaration at once, a space every
// interval (XML allows whitespace before the root element), then the result
// document (keepaliveBody). The metrics, audit log and event run once the
// outcome is known, and report it.
func streamPending(ctx fiber.Ctx, p *pendingResponse, s3action string, svc *Services, requestID, hostID string) {
	// The body reports through ctx after the handler has returned. fiber
	// never pools an abandoned ctx (it is garbage collected instead), so ctx
	// stays valid for as long as fasthttp is writing the body.
	ctx.Abandon()
	ctx.Response().Header.SetContentType(fiber.MIMEApplicationXML)
	ctx.Status(http.StatusOK)
	ctx.RequestCtx().SetBodyStream(&keepaliveBody{
		outcome: p.outcome,
		ticker:  time.NewTicker(p.interval),
		queued:  xmlhdr,
		finish: func(out pendingOutcome) []byte {
			return finishPending(ctx, out, s3action, svc, requestID, hostID)
		},
	}, -1)
}

// keepaliveBody is the body of a streamed pending response. fasthttp calls
// Read and Close on the connection's goroutine while it writes the
// response, and flushes each Read to the socket as its own chunk.
type keepaliveBody struct {
	outcome <-chan pendingOutcome
	ticker  *time.Ticker
	// finish records the outcome and returns the document that ends the body.
	finish func(pendingOutcome) []byte
	// queued holds the bytes Read has yet to return.
	queued []byte
	// finished is set once the outcome has been received and recorded.
	finished bool
}

var keepaliveSpace = []byte(" ")

func (b *keepaliveBody) Read(p []byte) (int, error) {
	for len(b.queued) == 0 {
		if b.finished {
			return 0, io.EOF
		}
		select {
		case out := <-b.outcome:
			b.queued = b.finish(out)
			b.finished = true
		case <-b.ticker.C:
			b.queued = keepaliveSpace
		}
	}
	n := copy(p, b.queued)
	b.queued = b.queued[n:]
	return n, nil
}

// Close ends the body. When the client went away mid-stream, Close still
// waits for the outcome and records it. Holding the request until then also
// keeps the backend's detached context valid (detachContext) and lets
// request-scoped state that ends with the request, such as a tracing span,
// cover the whole call.
func (b *keepaliveBody) Close() error {
	b.ticker.Stop()
	if !b.finished {
		b.finish(<-b.outcome)
		b.finished = true
	}
	return nil
}

// finishPending records a pending outcome, as ProcessController records an
// immediate one, and returns the document that ends the streamed body. The
// body has already committed to 200 and sent the XML declaration, so a
// failure can only be reported as an <Error> document in the body.
func finishPending(ctx fiber.Ctx, out pendingOutcome, s3action string, svc *Services, requestID, hostID string) []byte {
	response, err := out.response, out.err
	opts := response.MetaOpts
	if opts == nil {
		opts = &MetaOptions{}
	}
	sendMetrics(ctx, svc, err, s3action, opts)
	logMeta := s3log.LogMeta{
		Action:      s3action,
		BucketOwner: opts.BucketOwner,
		ObjectSize:  opts.ObjectSize,
	}
	if err != nil {
		if svc.Logger != nil {
			svc.Logger.Log(ctx, err, nil, logMeta)
		}
		return embeddedErrorBody(err, requestID, hostID)
	}

	sendEvent(ctx, svc, opts)
	body, err := xml.Marshal(response.Data)
	if err != nil {
		debuglogger.InternalError(err)
		if svc.Logger != nil {
			svc.Logger.Log(ctx, err, nil, logMeta)
		}
		return embeddedErrorBody(s3err.GetAPIError(s3err.ErrInternalError), requestID, hostID)
	}
	if svc.Logger != nil {
		svc.Logger.Log(ctx, nil, body, logMeta)
	}
	return body
}

// embeddedErrorBody is err's <Error> document without the XML declaration,
// which the stream has already sent. An error outside the S3 error table
// is an InternalError, as in ProcessController.
func embeddedErrorBody(err error, requestID, hostID string) []byte {
	serr, ok := err.(s3err.S3Error)
	if !ok {
		debuglogger.InternalError(err)
		serr = s3err.GetAPIError(s3err.ErrInternalError)
	}
	body := serr.XMLBody(requestID, hostID)
	doc, found := bytes.CutPrefix(body, xmlhdr)
	if !found {
		// Every s3err type encodes through encodeResponse, which writes the
		// declaration first. Sending the body as is keeps the error visible,
		// though a declaration past the stream's start makes it malformed.
		debuglogger.InternalError(fmt.Errorf("error document does not start with the XML declaration: %q", body))
		return body
	}
	return doc
}

// detachedContext carries a request's user values (what RequestCtx.Value
// returns) to backend work that may outlive the handler, without touching
// the RequestCtx: fasthttp forbids that from another goroutine, and the
// request's middleware may still set values on it after the handler
// returns. Like the RequestCtx, it is canceled only on server shutdown.
//
// The snapshot must not outlive the request, since fasthttp may reuse what
// the values point to afterwards. A pending response guarantees that:
// keepaliveBody.Close holds the request until the backend call returns.
type detachedContext struct {
	values []userValue
	done   <-chan struct{}
}

type userValue struct {
	key, value any
}

// detachContext snapshots rc's user values. It must run on the handler's
// goroutine, before the handler returns.
func detachContext(rc *fasthttp.RequestCtx) context.Context {
	d := &detachedContext{done: rc.Done()}
	rc.VisitUserValuesAll(func(key, value any) {
		d.values = append(d.values, userValue{key: key, value: value})
	})
	return d
}

func (d *detachedContext) Deadline() (time.Time, bool) { return time.Time{}, false }

func (d *detachedContext) Done() <-chan struct{} { return d.done }

func (d *detachedContext) Err() error {
	select {
	case <-d.done:
		return context.Canceled
	default:
		return nil
	}
}

// Value looks key up as fasthttp does: a []byte key matches its string form.
func (d *detachedContext) Value(key any) any {
	if b, ok := key.([]byte); ok {
		key = string(b)
	}
	for _, kv := range d.values {
		if kv.key == key {
			return kv.value
		}
	}
	return nil
}
