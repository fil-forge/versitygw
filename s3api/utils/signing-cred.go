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

import "time"

// SigningCred is the credential material SigV4 verification derives the
// signing key from: the account's raw secret access key, or a pre-derived
// signing key when the secret is held externally (e.g. in a KMS that only
// releases derived keys).
type SigningCred struct {
	// Secret is the raw secret access key.
	Secret string
	// SigningKey is a pre-derived SigV4 signing key: the AWS4 HMAC chain
	// already folded over the credential scope (date, region, service).
	// When non-empty it is used directly and Secret is ignored. A derived
	// key is only valid for the credential scope it was derived for.
	SigningKey []byte
}

// signingKey returns the pre-derived key when present, else derives one
// from the raw secret for the given scope.
func (c SigningCred) signingKey(region string, date time.Time) []byte {
	if len(c.SigningKey) > 0 {
		return c.SigningKey
	}
	return getSigningKey(c.Secret, region, date)
}
