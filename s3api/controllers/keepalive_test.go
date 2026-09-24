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
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/fil-forge/versitygw/metrics"
	"github.com/fil-forge/versitygw/s3api/utils"
	"github.com/fil-forge/versitygw/s3err"
	"github.com/fil-forge/versitygw/s3log"
	"github.com/fil-forge/versitygw/s3response"
	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

// The CompleteMultipartUpload keepalive tests drive the whole route through
// a real fasthttp server on an in-memory listener, so the chunking,
// flushing and connection lifetime they observe are fasthttp's own.

const (
	// Short enough for a slow completion to be streamed within a test.
	testKeepalive = 20 * time.Millisecond
	// Long enough for several keepalive intervals to pass.
	slowCompletion = 10 * testKeepalive
)

var (
	keepaliveETag         = `"mock-etag-3"`
	keepaliveCompleteBody = `<CompleteMultipartUpload><Part><PartNumber>1</PartNumber><ETag>ETag</ETag></Part></CompleteMultipartUpload>`
)

// requestScopedKey is a user value set ahead of the controller, standing in
// for the request-scoped state a backend reads from its context.
type requestScopedKey struct{}

// streamedResponse is what a client observes of a CompleteMultipartUpload
// response.
type streamedResponse struct {
	Status    int
	VersionID string
	// Declaration is the XML declaration the body starts with.
	Declaration string
	// Whitespace reports whether whitespace preceded the document.
	Whitespace bool
	Document   string
}

func TestCompleteMultipartUploadKeepalive_Responses(t *testing.T) {
	resultDoc := xmlDocument(t, s3response.CompleteMultipartUploadResult{
		Location: utils.GetStringPtr("http://s3.test/bucket/object"),
		ETag:     &keepaliveETag,
	})
	noSuchUploadDoc := errorDocument(s3err.GetAPIError(s3err.ErrNoSuchUpload))
	internalErrorDoc := errorDocument(s3err.GetAPIError(s3err.ErrInternalError))

	tests := []struct {
		name     string
		complete completeFunc
		want     streamedResponse
	}{
		{
			name:     "a completion within the interval is answered as before",
			complete: completeAfter(0, keepaliveETag, nil),
			want: streamedResponse{
				Status:      http.StatusOK,
				VersionID:   "version-1",
				Declaration: string(xmlhdr),
				Document:    resultDoc,
			},
		},
		{
			name:     "an error within the interval keeps its status",
			complete: completeAfter(0, "", s3err.GetAPIError(s3err.ErrNoSuchUpload)),
			want: streamedResponse{
				Status:      http.StatusNotFound,
				VersionID:   "version-1",
				Declaration: string(xmlhdr),
				Document:    noSuchUploadDoc,
			},
		},
		{
			name:     "a slow completion streams whitespace before the result",
			complete: completeAfter(slowCompletion, keepaliveETag, nil),
			want: streamedResponse{
				Status:      http.StatusOK,
				Declaration: string(xmlhdr),
				Whitespace:  true,
				Document:    resultDoc,
			},
		},
		{
			name:     "a slow S3 error is embedded in the 200",
			complete: completeAfter(slowCompletion, "", s3err.GetAPIError(s3err.ErrNoSuchUpload)),
			want: streamedResponse{
				Status:      http.StatusOK,
				Declaration: string(xmlhdr),
				Whitespace:  true,
				Document:    noSuchUploadDoc,
			},
		},
		{
			name:     "a slow non-S3 error is an embedded InternalError",
			complete: completeAfter(slowCompletion, "", errors.New("conclude failed")),
			want: streamedResponse{
				Status:      http.StatusOK,
				Declaration: string(xmlhdr),
				Whitespace:  true,
				Document:    internalErrorDoc,
			},
		},
		{
			name: "a slow backend panic is an embedded InternalError",
			complete: func(context.Context) (s3response.CompleteMultipartUploadResult, string, error) {
				time.Sleep(slowCompletion)
				panic("boom")
			},
			want: streamedResponse{
				Status:      http.StatusOK,
				Declaration: string(xmlhdr),
				Whitespace:  true,
				Document:    internalErrorDoc,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := startKeepaliveServer(t, tt.complete)
			resp, err := srv.client.Do(completeRequest(t))
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, tt.want, readStreamedResponse(t, resp))
		})
	}
}

// completeFunc is a backend's CompleteMultipartUpload.
type completeFunc func(context.Context) (s3response.CompleteMultipartUploadResult, string, error)

// completeAfter returns a completion that takes delay and then succeeds
// with etag and version id "version-1", or fails with err.
func completeAfter(delay time.Duration, etag string, err error) completeFunc {
	return func(context.Context) (s3response.CompleteMultipartUploadResult, string, error) {
		time.Sleep(delay)
		if err != nil {
			return s3response.CompleteMultipartUploadResult{}, "version-1", err
		}
		return s3response.CompleteMultipartUploadResult{ETag: &etag}, "version-1", nil
	}
}

// backendContext is what a completion sees of its context.
type backendContext struct {
	RequestScoped any
	IsRequestCtx  bool
}

func TestCompleteMultipartUploadKeepalive_BackendContext(t *testing.T) {
	delays := map[string]time.Duration{
		"within the interval": 0,
		"past the interval":   slowCompletion,
	}
	for name, delay := range delays {
		t.Run("carries the request's values but not its RequestCtx "+name, func(t *testing.T) {
			seen := make(chan backendContext, 1)
			srv := startKeepaliveServer(t, func(ctx context.Context) (s3response.CompleteMultipartUploadResult, string, error) {
				_, isRequestCtx := ctx.(*fasthttp.RequestCtx)
				seen <- backendContext{RequestScoped: ctx.Value(requestScopedKey{}), IsRequestCtx: isRequestCtx}
				return completeAfter(delay, keepaliveETag, nil)(ctx)
			})
			resp, err := srv.client.Do(completeRequest(t))
			require.NoError(t, err)
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()

			assert.Equal(t, backendContext{RequestScoped: "request-scoped"}, <-seen)
		})
	}
}

func TestCompleteMultipartUploadKeepalive_AuditsTheOutcome(t *testing.T) {
	backendErr := errors.New("conclude failed")
	srv := startKeepaliveServer(t, completeAfter(slowCompletion, "", backendErr))
	resp, err := srv.client.Do(completeRequest(t))
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	assert.Equal(t, []auditRecord{{Action: metrics.ActionCompleteMultipartUpload, Err: backendErr}}, srv.shutDown(t))
}

func TestCompleteMultipartUploadKeepalive_OutlivesADisconnectedClient(t *testing.T) {
	release := make(chan struct{})
	var completed atomic.Bool
	srv := startKeepaliveServer(t, func(ctx context.Context) (s3response.CompleteMultipartUploadResult, string, error) {
		<-release
		completed.Store(true)
		return completeAfter(0, keepaliveETag, nil)(ctx)
	})
	resp, err := srv.client.Do(completeRequest(t))
	require.NoError(t, err)
	// The declaration arrives once the response has turned into a stream;
	// then the client goes away, with the completion still running. The
	// server notices on its next keepalive write, which fails and closes
	// the body while the completion is still pending.
	_, err = io.ReadFull(resp.Body, make([]byte, len(xmlhdr)))
	require.NoError(t, err)
	resp.Body.Close()
	time.Sleep(5 * testKeepalive)
	close(release)

	records := srv.shutDown(t)
	assert.Equal(t, auditedCompletion{
		Completed: true,
		Records:   []auditRecord{{Action: metrics.ActionCompleteMultipartUpload}},
	}, auditedCompletion{Completed: completed.Load(), Records: records})
}

// auditedCompletion is whether a completion ran to its end, and what the
// audit log recorded of it.
type auditedCompletion struct {
	Completed bool
	Records   []auditRecord
}

func TestCompleteMultipartUploadKeepalive_SDKClient(t *testing.T) {
	t.Run("decodes a streamed result", func(t *testing.T) {
		srv := startKeepaliveServer(t, completeAfter(slowCompletion, keepaliveETag, nil))
		out, err := srv.sdkClient().CompleteMultipartUpload(t.Context(), sdkCompleteInput())
		require.NoError(t, err)

		assert.Equal(t, keepaliveETag, aws.ToString(out.ETag))
	})

	t.Run("retries a streamed error", func(t *testing.T) {
		var calls atomic.Int32
		srv := startKeepaliveServer(t, func(ctx context.Context) (s3response.CompleteMultipartUploadResult, string, error) {
			calls.Add(1)
			return completeAfter(slowCompletion, "", errors.New("conclude failed"))(ctx)
		})
		_, err := srv.sdkClient().CompleteMultipartUpload(t.Context(), sdkCompleteInput())
		require.Error(t, err)

		assert.Greater(t, calls.Load(), int32(1))
	})
}

// keepaliveServer serves the CompleteMultipartUpload route with the
// keepalive on, over an in-memory listener.
type keepaliveServer struct {
	app    *fiber.App
	client *http.Client
	audit  *recordingAuditLogger
}

func startKeepaliveServer(t *testing.T, complete completeFunc) *keepaliveServer {
	t.Helper()
	be := &BackendMock{
		CompleteMultipartUploadFunc: func(ctx context.Context, _ *s3.CompleteMultipartUploadInput) (s3response.CompleteMultipartUploadResult, string, error) {
			return complete(ctx)
		},
		GetBucketPolicyFunc: func(context.Context, string) ([]byte, error) {
			return nil, s3err.GetAPIError(s3err.ErrAccessDenied)
		},
		GetObjectLockConfigurationFunc: func(context.Context, string) ([]byte, error) {
			return nil, s3err.GetAPIError(s3err.ErrObjectLockConfigurationNotFound)
		},
		GetBucketVersioningFunc: func(context.Context, string) (s3response.GetBucketVersioningOutput, error) {
			return s3response.GetBucketVersioningOutput{}, s3err.GetAPIError(s3err.ErrNotImplemented)
		},
	}
	ctrl := S3ApiController{be: be, completeMpKeepalive: testKeepalive}
	audit := &recordingAuditLogger{records: make(chan auditRecord, 16)}
	svc := &Services{Logger: audit}

	app := fiber.New()
	app.Post("/:bucket/*", ProcessHandlers(ctrl.CompleteMultipartUpload, metrics.ActionCompleteMultipartUpload, svc,
		func(ctx fiber.Ctx) error {
			for key, local := range defaultLocals {
				key.Set(ctx, local)
			}
			utils.ContextKeyRequestID.Set(ctx, testRequestID)
			utils.ContextKeyHostID.Set(ctx, testHostID)
			ctx.RequestCtx().SetUserValue(requestScopedKey{}, "request-scoped")
			return nil
		}))

	ln := fasthttputil.NewInmemoryListener()
	served := make(chan error, 1)
	go func() {
		served <- app.Listener(ln, fiber.ListenConfig{DisableStartupMessage: true})
	}()
	srv := &keepaliveServer{
		app: app,
		client: &http.Client{Transport: &http.Transport{
			DialContext: func(context.Context, string, string) (net.Conn, error) { return ln.Dial() },
		}},
		audit: audit,
	}
	t.Cleanup(func() {
		srv.client.CloseIdleConnections()
		_ = app.ShutdownWithTimeout(5 * time.Second)
		<-served
	})
	return srv
}

// shutDown stops the server, which waits for every response to finish
// writing, and returns what the audit log recorded.
func (s *keepaliveServer) shutDown(t *testing.T) []auditRecord {
	t.Helper()
	s.client.CloseIdleConnections()
	require.NoError(t, s.app.ShutdownWithTimeout(5*time.Second))
	close(s.audit.records)
	var records []auditRecord
	for r := range s.audit.records {
		records = append(records, r)
	}
	return records
}

// sdkClient is an AWS SDK client for the server that retries without
// meaningful backoff.
func (s *keepaliveServer) sdkClient() *s3.Client {
	return s3.New(s3.Options{
		BaseEndpoint: aws.String("http://s3.test"),
		UsePathStyle: true,
		Region:       "us-east-1",
		Credentials:  credentials.NewStaticCredentialsProvider("access", "secret", ""),
		HTTPClient:   s.client,
		Retryer: retry.NewStandard(func(o *retry.StandardOptions) {
			o.MaxBackoff = time.Millisecond
		}),
	})
}

func sdkCompleteInput() *s3.CompleteMultipartUploadInput {
	return &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String("bucket"),
		Key:      aws.String("object"),
		UploadId: aws.String("upload-1"),
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: []types.CompletedPart{{PartNumber: aws.Int32(1), ETag: aws.String("ETag")}},
		},
	}
}

func completeRequest(t *testing.T) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		"http://s3.test/bucket/object?uploadId=upload-1", strings.NewReader(keepaliveCompleteBody))
	require.NoError(t, err)
	return req
}

// readStreamedResponse reads resp to the end and splits its body into the
// declaration, the whitespace after it, and the document.
func readStreamedResponse(t *testing.T, resp *http.Response) streamedResponse {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	declaration, rest := body, []byte(nil)
	if i := bytes.IndexByte(body, '\n'); i >= 0 {
		declaration, rest = body[:i+1], body[i+1:]
	}
	document := bytes.TrimLeft(rest, " ")
	return streamedResponse{
		Status:      resp.StatusCode,
		VersionID:   resp.Header.Get("x-amz-version-id"),
		Declaration: string(declaration),
		Whitespace:  len(document) < len(rest),
		Document:    string(document),
	}
}

// xmlDocument is v's XML encoding without a declaration.
func xmlDocument(t *testing.T, v any) string {
	t.Helper()
	doc, err := xml.Marshal(v)
	require.NoError(t, err)
	return string(doc)
}

// errorDocument is err's <Error> document for the test request, without
// its declaration.
func errorDocument(err s3err.APIError) string {
	return strings.TrimPrefix(string(err.XMLBody(testRequestID, testHostID)), string(xmlhdr))
}

type auditRecord struct {
	Action string
	Err    error
}

// recordingAuditLogger records every audit log call's action and error.
type recordingAuditLogger struct {
	records chan auditRecord
}

func (l *recordingAuditLogger) Log(_ fiber.Ctx, err error, _ []byte, meta s3log.LogMeta) {
	l.records <- auditRecord{Action: meta.Action, Err: err}
}
func (l *recordingAuditLogger) HangUp() error   { return nil }
func (l *recordingAuditLogger) Shutdown() error { return nil }
