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

package s3response

import (
	"encoding/xml"
	"testing"
)

// TestDeleteObjectsUnmarshalQuiet locks parsing of the <Quiet> flag from a
// DeleteObjects request body. Without it the controller cannot honor quiet
// mode and always returns the Deleted list.
func TestDeleteObjectsUnmarshalQuiet(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"quiet true", `<Delete><Object><Key>k</Key></Object><Quiet>true</Quiet></Delete>`, true},
		{"quiet false", `<Delete><Object><Key>k</Key></Object><Quiet>false</Quiet></Delete>`, false},
		{"quiet absent", `<Delete><Object><Key>k</Key></Object></Delete>`, false},
	}
	for _, c := range cases {
		var d DeleteObjects
		if err := xml.Unmarshal([]byte(c.body), &d); err != nil {
			t.Fatalf("%s: unmarshal: %v", c.name, err)
		}
		if d.Quiet != c.want {
			t.Errorf("%s: Quiet = %v, want %v", c.name, d.Quiet, c.want)
		}
	}
}
