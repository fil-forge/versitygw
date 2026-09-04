// Copyright 2025 Versity Software
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

package utils_test

import (
	"testing"

	"github.com/fil-forge/versitygw/s3api/utils"
)

func TestContainsC1ControlChar(t *testing.T) {
	cases := []struct {
		name string
		key  string
		want bool
	}{
		{"c1 control U+008A (object-crud-0037 key)", "®-", true},
		{"crlf is c0, allowed", "test\r\nCRLF.txt", false},
		{"escape is c0, allowed", "testescape.txt", false},
		{"registered sign is above the c1 range", "®-plain", false},
		{"plain ascii", "hello/world.txt", false},
		{"empty", "", false},
	}
	for _, c := range cases {
		if got := utils.ContainsC1ControlChar(c.key); got != c.want {
			t.Errorf("%s: ContainsC1ControlChar(%q) = %v, want %v", c.name, c.key, got, c.want)
		}
	}
}
