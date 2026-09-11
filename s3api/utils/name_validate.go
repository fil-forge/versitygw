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

package utils

func IsObjectNameValid(name string) bool {
	return IsObjectNameValidWithTraversal(name, true)
}

// IsObjectNameValidWithTraversal validates an object key. The empty/"."/".."/"/"
// rejections always apply; the path-traversal (non-local) rejection applies only
// when checkTraversal is set. Backends that store keys as opaque strings rather
// than filesystem paths pass false, since a key like "../file.txt" is a legal
// literal S3 key for them and carries no traversal risk.
func IsObjectNameValidWithTraversal(name string, checkTraversal bool) bool {
	if name == "" {
		return false
	}

	// Opaque backends store keys as literal strings, so any non-empty key is
	// valid — AWS accepts "/", "//", ".", ".." and the like as object keys.
	if !checkTraversal {
		return true
	}

	switch clean(name) {
	case "", ".", "..", "/":
		return false
	}

	return isObjectLocal(name)
}
