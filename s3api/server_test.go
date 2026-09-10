// Copyright 2023 Versity Software
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

package s3api

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fil-forge/versitygw/auth"
	"github.com/fil-forge/versitygw/backend"
	"github.com/fil-forge/versitygw/s3api/middlewares"
	"github.com/fil-forge/versitygw/s3api/utils"
	"github.com/gofiber/fiber/v3"
	"github.com/valyala/fasthttp/fasthttputil"
)

func newTestS3ApiServer(opts ...Option) (*S3ApiServer, error) {
	allOpts := append([]Option{WithConcurrencyLimiter(10, 10)}, opts...)

	return New(
		backend.BackendUnsupported{},
		middlewares.RootUserConfig{Access: "access", Secret: "secret"},
		"us-east-1",
		auth.NewIAMServiceSingle(auth.Account{Access: "access", Secret: "secret"}),
		nil,
		nil,
		nil,
		nil,
		allOpts...,
	)
}

func TestS3ApiServer_Serve(t *testing.T) {
	tests := []struct {
		name    string
		sa      *S3ApiServer
		wantErr bool
		port    string
	}{
		{
			name:    "Serve-invalid-tcp-address",
			wantErr: true,
			sa: &S3ApiServer{
				app:     fiber.New(),
				backend: backend.BackendUnsupported{},
				Router:  &S3ApiRouter{},
			},
			port: "localhost:notaport",
		},
		{
			name:    "Serve-invalid-tcp-address-with-certificate",
			wantErr: true,
			sa: &S3ApiServer{
				app:         fiber.New(),
				backend:     backend.BackendUnsupported{},
				Router:      &S3ApiRouter{},
				CertStorage: &utils.CertStorage{},
			},
			port: "localhost:notaport",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.sa.ServeMultiPort([]string{tt.port}); (err != nil) != tt.wantErr {
				t.Errorf("S3ApiServer.Serve() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestWithRouteRegistersBeforeMiddleware(t *testing.T) {
	const routePath = "/custom/route"

	middlewareCalled := false
	server, err := newTestS3ApiServer(
		WithRoute(http.MethodGet, routePath, func(ctx fiber.Ctx) error {
			return ctx.SendStatus(http.StatusNoContent)
		}),
		WithMiddleware("/", func(ctx fiber.Ctx) error {
			middlewareCalled = true
			return ctx.SendStatus(http.StatusMisdirectedRequest)
		}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	resp, err := server.app.Test(httptest.NewRequest(http.MethodGet, routePath, nil))
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Fatalf("response close error = %v", err)
		}
	}()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}
	if middlewareCalled {
		t.Fatal("middleware was called for top-level route")
	}
}

func TestWithRouteRegistersAfterRateLimiter(t *testing.T) {
	const routePath = "/custom/limited"

	started := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	var once sync.Once

	server, err := newTestS3ApiServer(
		WithConcurrencyLimiter(10, 1),
		WithRoute(http.MethodGet, routePath, func(ctx fiber.Ctx) error {
			once.Do(func() {
				close(started)
			})
			<-release
			return ctx.SendStatus(http.StatusNoContent)
		}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	go func() {
		resp, err := server.app.Test(httptest.NewRequest(http.MethodGet, routePath, nil), fiber.TestConfig{Timeout: 0, FailOnTimeout: false})
		if err != nil {
			firstDone <- err
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			firstDone <- fiber.NewError(resp.StatusCode)
			return
		}
		firstDone <- nil
	}()

	<-started

	resp, err := server.app.Test(httptest.NewRequest(http.MethodGet, routePath, nil), fiber.TestConfig{Timeout: time.Duration(100) * time.Millisecond})
	if err != nil {
		close(release)
		t.Fatalf("second app.Test() error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		close(release)
		t.Fatalf("second status = %d, want %d", resp.StatusCode, http.StatusServiceUnavailable)
	}

	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first request error = %v", err)
	}
}

func TestCustomMountValidation(t *testing.T) {
	validHandler := func(ctx fiber.Ctx) error {
		return ctx.SendStatus(http.StatusNoContent)
	}

	tests := []struct {
		name    string
		opt     Option
		wantErr string
	}{
		{
			name:    "route empty method",
			opt:     WithRoute("", "/custom", validHandler),
			wantErr: "empty method",
		},
		{
			name:    "route unsupported HTTP method",
			opt:     WithRoute("BREW", "/custom", validHandler),
			wantErr: "invalid HTTP method",
		},
		{
			name:    "route empty path",
			opt:     WithRoute(http.MethodGet, "", validHandler),
			wantErr: "must start with /",
		},
		{
			name:    "route relative path",
			opt:     WithRoute(http.MethodGet, "custom", validHandler),
			wantErr: "must start with /",
		},
		{
			name:    "route no handlers",
			opt:     WithRoute(http.MethodGet, "/custom"),
			wantErr: "no handlers",
		},
		{
			name:    "route nil handler",
			opt:     WithRoute(http.MethodGet, "/custom", fiber.Handler(nil)),
			wantErr: "nil handler",
		},
		{
			name:    "middleware empty prefix",
			opt:     WithMiddleware("", validHandler),
			wantErr: "must start with /",
		},
		{
			name:    "middleware relative prefix",
			opt:     WithMiddleware("custom", validHandler),
			wantErr: "must start with /",
		},
		{
			name:    "middleware nil handler",
			opt:     WithMiddleware("/custom", nil),
			wantErr: "nil handler",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newTestS3ApiServer(tt.opt)
			if err == nil {
				t.Fatal("New() error = nil, want error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("New() error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

// countingReader counts the bytes a client actually sends as a request body.
type countingReader struct {
	n atomic.Int64
}

func (r *countingReader) Read(p []byte) (int, error) {
	r.n.Add(int64(len(p)))
	return len(p), nil
}

// TestExpectContinue_RejectsOverCapBeforeBody: an upload declaring more than
// the 5 GiB cap with "Expect: 100-continue" is answered EntityTooLarge before
// the client sends a single body byte; one within the cap gets its 100
// Continue and reaches the handlers. Runs the real fasthttp server over an
// in-memory listener, since the Expect exchange happens below fiber.
func TestExpectContinue_RejectsOverCapBeforeBody(t *testing.T) {
	server, err := newTestS3ApiServer()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ln := fasthttputil.NewInmemoryListener()
	go func() { _ = server.app.Server().Serve(ln) }()
	t.Cleanup(func() { _ = ln.Close() })

	client := &http.Client{Transport: &http.Transport{
		DialContext:           func(context.Context, string, string) (net.Conn, error) { return ln.Dial() },
		ExpectContinueTimeout: 5 * time.Second,
	}}
	send := func(size int64) (*http.Response, *countingReader) {
		body := &countingReader{}
		req, err := http.NewRequest(http.MethodPut, "http://vgw/bucket/key", io.LimitReader(body, size))
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.ContentLength = size
		req.Header.Set("Expect", "100-continue")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("PUT of %d bytes: %v", size, err)
		}
		return resp, body
	}

	resp, body := send(utils.MaxObjSizeLimit + 1)
	xmlBody, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("over-cap status = %d, want 400", resp.StatusCode)
	}
	if !strings.Contains(string(xmlBody), "<Code>EntityTooLarge</Code>") {
		t.Fatalf("over-cap body = %q, want an EntityTooLarge error document", xmlBody)
	}
	if resp.Header.Get(utils.HeaderAmzRequestID) == "" {
		t.Fatalf("over-cap response lacks %s", utils.HeaderAmzRequestID)
	}
	if got := body.n.Load(); got != 0 {
		t.Fatalf("client sent %d body bytes before the rejection, want 0", got)
	}

	// Within the cap the Expect handler steps aside: the request reaches the
	// S3 handlers, which reject the unsigned request as they would any other.
	resp, _ = send(1024)
	within, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if strings.Contains(string(within), "EntityTooLarge") || resp.StatusCode == http.StatusExpectationFailed {
		t.Fatalf("within-cap request was refused at the Expect stage: status %d body %q", resp.StatusCode, within)
	}
}
