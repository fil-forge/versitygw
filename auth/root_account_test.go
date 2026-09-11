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

package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsRootAccess(t *testing.T) {
	root := Account{Access: "root", Secret: "secret", Role: RoleAdmin}
	assert.True(t, isRootAccess(root, "root"))
	assert.False(t, isRootAccess(root, "other"))
	assert.False(t, isRootAccess(root, ""))

	// A gateway without a root account holds the zero Account: nothing
	// matches it, least of all an empty access key.
	assert.False(t, isRootAccess(Account{}, ""))
	assert.False(t, isRootAccess(Account{}, "root"))
}

// TestIAMServiceSingle_NoRoot pins the rootless single-account service: an
// empty access key is not the (zero) root account.
func TestIAMServiceSingle_NoRoot(t *testing.T) {
	svc := NewIAMServiceSingle(Account{})
	_, err := svc.GetUserAccount("")
	assert.Error(t, err)
}

// TestCheckIfAccountsExist_EmptyID pins that an empty account id is reported
// missing without consulting IAM, so admin paths such as ChangeBucketOwner
// cannot hand out an empty owner in rootless mode.
func TestCheckIfAccountsExist_EmptyID(t *testing.T) {
	missing, err := CheckIfAccountsExist([]string{""}, NewIAMServiceSingle(Account{}))
	assert.NoError(t, err)
	assert.Equal(t, []string{""}, missing)
}

// TestValidateNewAccount pins that no IAM backend persists an account with an
// empty access key: request authentication rejects such a key before any
// lookup, so the entry could never be used.
func TestValidateNewAccount(t *testing.T) {
	assert.NoError(t, validateNewAccount(Account{Access: "user", Secret: "secret", Role: RoleUser}))
	assert.Error(t, validateNewAccount(Account{Secret: "secret", Role: RoleUser}))
}

// TestIAMServiceSingle_EmptyAccess pins the lookup guard shared by every
// backend: an empty access key is not found, whatever the root account.
func TestIAMServiceSingle_EmptyAccess(t *testing.T) {
	for name, root := range map[string]Account{
		"root disabled": {},
		"root enabled":  {Access: "root", Secret: "secret", Role: RoleAdmin},
	} {
		_, err := NewIAMServiceSingle(root).GetUserAccount("")
		assert.Error(t, err, name)
	}
}
