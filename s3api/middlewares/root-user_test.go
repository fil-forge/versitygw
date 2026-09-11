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

package middlewares

import (
	"errors"
	"testing"

	"github.com/fil-forge/versitygw/auth"
	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
)

// recordingIAM is an auth.IAMService that records the access keys looked up
// and knows no accounts.
type recordingIAM struct {
	auth.IAMService
	lookups []string
}

func (r *recordingIAM) GetUserAccount(access string) (auth.Account, error) {
	r.lookups = append(r.lookups, access)
	return auth.Account{}, auth.ErrNoSuchUser
}

func TestRootUserConfig_Matches(t *testing.T) {
	enabled := RootUserConfig{Access: "root", Secret: "secret"}
	assert.True(t, enabled.Enabled())
	assert.True(t, enabled.matches("root"))
	assert.False(t, enabled.matches("other"))
	assert.False(t, enabled.matches(""))

	// A zero-value config disables root: nothing matches, least of all an
	// empty access key, which would otherwise verify against an empty secret.
	disabled := RootUserConfig{}
	assert.False(t, disabled.Enabled())
	assert.False(t, disabled.matches(""))
	assert.False(t, disabled.matches("root"))
}

func TestAccounts_GetAccount_RootDisabled(t *testing.T) {
	iam := &recordingIAM{}
	acct := accounts{root: RootUserConfig{}, iam: iam}

	for _, access := range []string{"", "root", "someone"} {
		_, err := acct.getAccount(fiber.Ctx(nil), access)
		assert.True(t, errors.Is(err, auth.ErrNoSuchUser), "access %q must resolve through IAM", access)
	}
	assert.Equal(t, []string{"", "root", "someone"}, iam.lookups)
}

func TestAccounts_GetAccount_RootEnabled(t *testing.T) {
	iam := &recordingIAM{}
	acct := accounts{root: RootUserConfig{Access: "root", Secret: "secret"}, iam: iam}

	got, err := acct.getAccount(fiber.Ctx(nil), "root")
	assert.NoError(t, err)
	assert.Equal(t, auth.Account{Access: "root", Secret: "secret", Role: auth.RoleAdmin}, got)
	assert.Empty(t, iam.lookups, "the root key never reaches IAM")

	_, err = acct.getAccount(fiber.Ctx(nil), "")
	assert.True(t, errors.Is(err, auth.ErrNoSuchUser))
	assert.Equal(t, []string{""}, iam.lookups)
}
