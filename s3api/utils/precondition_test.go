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

package utils

import "testing"

func TestParsePreconditionDateHeader(t *testing.T) {
	if got := ParsePreconditionDateHeader(""); got != nil {
		t.Errorf("empty date = %v, want nil", got)
	}
	if got := ParsePreconditionDateHeader("not a date"); got != nil {
		t.Errorf("unparseable date = %v, want nil", got)
	}
	if got := ParsePreconditionDateHeader("Mon, 02 Jan 2006 15:04:05 GMT"); got == nil {
		t.Error("past RFC1123 date parsed to nil, want a time")
	}
	if got := ParsePreconditionDateHeader("2006-01-02T15:04:05Z"); got == nil {
		t.Error("RFC3339 date parsed to nil, want a time")
	}

	// A future date is dropped (returns nil): S3 does not evaluate a future
	// If-Modified-Since / If-Unmodified-Since — verified against AWS S3, where a
	// future If-Modified-Since on an unmodified object returns 200, not 304.
	if got := ParsePreconditionDateHeader("Fri, 29 Oct 2100 19:43:31 GMT"); got != nil {
		t.Errorf("future date = %v, want nil (future dates are ignored)", got)
	}
}
