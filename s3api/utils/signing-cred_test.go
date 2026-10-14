// Copyright 2026 Versity Software
// This file is licensed under the Apache License, Version 2.0
// (the "License"); you may not use this file except in compliance
// with the License.  You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package utils

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// derivedTestKey derives the SigV4 signing key for the test credentials'
// scope, standing in for a key released by an external system (e.g. a KMS)
// in place of the raw secret.
func derivedTestKey(t *testing.T, secret string, signingTime time.Time) []byte {
	t.Helper()
	return deriveSigningKey(secret, signingTime.Format(yyyymmdd), signedHeadersTestRegion)
}

func TestSigningCredSigningKey(t *testing.T) {
	date := time.Now().UTC()

	t.Run("pre-derived key is used as-is", func(t *testing.T) {
		key := []byte("pre-derived")
		cred := SigningCred{Secret: "ignored", SigningKey: key}
		require.Equal(t, key, cred.signingKey(signedHeadersTestRegion, date))
	})

	t.Run("derives from secret when no key", func(t *testing.T) {
		cred := SigningCred{Secret: "SECRET"}
		require.Equal(t,
			getSigningKey("SECRET", signedHeadersTestRegion, date),
			cred.signingKey(signedHeadersTestRegion, date))
	})
}

func TestCheckValidSignatureWithDerivedKey(t *testing.T) {
	ctx, authData, signingTime := signedHeaderAuthCtx(t, nil, nil)

	t.Run("valid derived key, no secret", func(t *testing.T) {
		cred := SigningCred{SigningKey: derivedTestKey(t, signedHeadersTestCreds.SecretAccessKey, signingTime)}
		_, err := CheckValidSignature(ctx, authData, cred, unsignedPayload, signingTime, 0)
		require.NoError(t, err)
	})

	t.Run("derived key from wrong secret is rejected", func(t *testing.T) {
		cred := SigningCred{SigningKey: derivedTestKey(t, "WRONG", signingTime)}
		_, err := CheckValidSignature(ctx, authData, cred, unsignedPayload, signingTime, 0)
		require.Error(t, err)
	})

	t.Run("derived key takes precedence over secret", func(t *testing.T) {
		cred := SigningCred{
			Secret:     "WRONG",
			SigningKey: derivedTestKey(t, signedHeadersTestCreds.SecretAccessKey, signingTime),
		}
		_, err := CheckValidSignature(ctx, authData, cred, unsignedPayload, signingTime, 0)
		require.NoError(t, err)
	})
}

func TestCheckPresignedSignatureWithDerivedKey(t *testing.T) {
	signedURL := buildPresignedURL(t, nil)
	ctx := fiberCtxFromURL(t, http.MethodPut, signedURL, nil)
	authData, err := ParsePresignedURIParts(ctx, signedHeadersTestRegion)
	require.NoError(t, err)

	signingTime, err := time.Parse(iso8601Format, authData.Date)
	require.NoError(t, err)

	t.Run("valid derived key, no secret", func(t *testing.T) {
		cred := SigningCred{SigningKey: derivedTestKey(t, signedHeadersTestCreds.SecretAccessKey, signingTime)}
		require.NoError(t, CheckPresignedSignature(ctx, authData, cred))
	})

	t.Run("derived key from wrong secret is rejected", func(t *testing.T) {
		cred := SigningCred{SigningKey: derivedTestKey(t, "WRONG", signingTime)}
		require.Error(t, CheckPresignedSignature(ctx, authData, cred))
	})
}

func TestSignPostPolicyWithDerivedKey(t *testing.T) {
	const policy = "eyJleHBpcmF0aW9uIjoiMjAyNi0wMS0wMVQwMDowMDowMFoifQ=="
	date := time.Now().UTC().Format(yyyymmdd)

	fromSecret, err := SignPostPolicy(policy, date, signedHeadersTestRegion, SigningCred{Secret: "SECRET"})
	require.NoError(t, err)

	fromKey, err := SignPostPolicy(policy, date, signedHeadersTestRegion, SigningCred{
		SigningKey: deriveSigningKey("SECRET", date, signedHeadersTestRegion),
	})
	require.NoError(t, err)

	require.Equal(t, fromSecret, fromKey)
}
