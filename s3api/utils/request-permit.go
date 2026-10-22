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

package utils

import (
	"sync"

	"github.com/gofiber/fiber/v3"
)

// RequestPermit is a request's slot under the server's request limit
// (middlewares.RateLimiter). The limiter releases it when the handler
// returns, unless the handler holds it for work that outlives the handler,
// such as a streamed response whose backend call is still running. The
// holder then releases it.
type RequestPermit struct {
	release func()
	once    sync.Once
	// held is set by Hold. Both Hold and Done run on the handler's
	// goroutine.
	held bool
}

// NewRequestPermit returns a permit that frees its slot with release.
func NewRequestPermit(release func()) *RequestPermit {
	return &RequestPermit{release: release}
}

// Done releases p when the handler returns, unless the handler holds it.
func (p *RequestPermit) Done() {
	if !p.held {
		p.free()
	}
}

func (p *RequestPermit) free() {
	p.once.Do(p.release)
}

// HoldRequestPermit keeps ctx's request permit past the handler's return
// and returns the function that releases it, which may run on any
// goroutine. Without a request limit it returns a no-op.
func HoldRequestPermit(ctx fiber.Ctx) func() {
	p, ok := ContextKeyRequestPermit.Get(ctx).(*RequestPermit)
	if !ok {
		return func() {}
	}
	p.held = true
	return p.free
}
