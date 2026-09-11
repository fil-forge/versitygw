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

package middlewares

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strconv"
	"time"

	"github.com/fil-forge/versitygw/auth"
	"github.com/fil-forge/versitygw/s3api/utils"
	"github.com/fil-forge/versitygw/s3err"
	"github.com/gofiber/fiber/v3"
)

const (
	iso8601Format = "20060102T150405Z"
	defaultRegion = "us-east-1"
)

// RootUserConfig is the gateway's built-in root account: an access key the
// auth middlewares resolve directly, ahead of the IAM service, with the admin
// role and every ACL / policy check skipped. A zero-value config disables the
// root account, so every access key (an empty one included) resolves through
// the IAM service.
type RootUserConfig struct {
	Access string
	Secret string
}

// Enabled reports whether a root account is configured.
func (r RootUserConfig) Enabled() bool {
	return r.Access != ""
}

// matches reports whether access names the root account. It is false when
// the root account is disabled: an empty access key must never match an
// empty root key, since that would verify the signature against an empty
// secret and grant root.
func (r RootUserConfig) matches(access string) bool {
	return r.Enabled() && access == r.Access
}

func VerifyV4Signature(root RootUserConfig, iam auth.IAMService, region string, streamBody, requireContentSha256, allowDefaultRegion bool) fiber.Handler {
	acct := accounts{root: root, iam: iam}

	return func(ctx fiber.Ctx) error {
		// The bucket is public, no need to check this signature
		if utils.ContextKeyPublicBucket.IsSet(ctx) {
			return nil
		}
		// If ContextKeyAuthenticated is set in context locals, it means it was presigned url case
		if utils.ContextKeyAuthenticated.IsSet(ctx) {
			return nil
		}

		// A present but malformed Authorization header is rejected before the
		// date check so it surfaces as InvalidArgument (AWS) rather than the
		// missing-date AccessDenied.
		if hdr := ctx.Get("Authorization"); hdr != "" {
			if _, err := utils.ParseAuthorization(hdr); err != nil {
				return err
			}
		}

		// Check X-Amz-Date header
		date := ctx.Get("X-Amz-Date")
		if date == "" {
			// Fall back to `Date` header if `X-Amz-Date` is not set
			date = ctx.Get("Date")
		}
		if date == "" {
			return s3err.GetAPIError(s3err.ErrMissingDateHeader)
		}

		// Parse the date and check the date validity
		tdate, err := time.Parse(iso8601Format, date)
		if err != nil {
			return s3err.GetAPIError(s3err.ErrMissingDateHeader)
		}

		// Validate the dates difference
		err = utils.ValidateDate(tdate)
		if err != nil {
			return err
		}

		authorization := ctx.Get("Authorization")
		if authorization == "" {
			return s3err.GetInvalidArgumentErr(s3err.InvalidArgAuthHeader, authorization)
		}

		authData, err := utils.ParseAuthorization(authorization)
		if err != nil {
			return err
		}

		if authData.Region != region && !(allowDefaultRegion && authData.Region == defaultRegion) {
			return s3err.MalformedAuth.IncorrectRegion(region, authData.Region)
		}

		utils.ContextKeyIsRoot.Set(ctx, root.matches(authData.Access))

		account, err := acct.getAccount(ctx, authData.Access)
		if err == auth.ErrNoSuchUser {
			return s3err.GetInvalidAccessKeyIdErr(authData.Access)
		}
		if err != nil {
			return err
		}

		if date[:8] != authData.Date {
			return s3err.MalformedAuth.DateMismatch()
		}

		utils.ContextKeyAccount.Set(ctx, account)

		var contentLength int64
		contentLengthStr := ctx.Get("Content-Length")
		if contentLengthStr != "" {
			contentLength, err = strconv.ParseInt(contentLengthStr, 10, 64)
			//TODO: not sure if InvalidRequest should be returned in this case
			if err != nil {
				return s3err.GetAPIError(s3err.ErrInvalidRequest)
			}
		}

		hashPayload := ctx.Get("X-Amz-Content-Sha256")
		if requireContentSha256 && hashPayload == "" {
			return s3err.GetAPIError(s3err.ErrMissingContentSha256)
		}
		if !utils.IsValidSha256PayloadHeader(hashPayload) {
			return s3err.GetInvalidArgumentErr(s3err.InvalidArgSHA256Payload, hashPayload)
		}
		// the streaming payload type is allowed only in PutObject and UploadPart
		// e.g. STREAMING-UNSIGNED-PAYLOAD-TRAILER
		if !streamBody && utils.IsStreamingPayload(hashPayload) {
			return s3err.GetAPIError(s3err.ErrInvalidSHA256PayloadUsage)
		}

		canonicalString, err := utils.CheckValidSignature(ctx, authData, signingCred(account), hashPayload, tdate, contentLength)
		if err != nil {
			return err
		}

		if streamBody {
			// store the request body stream reader in context locals
			wrapBodyReader(ctx, func(r io.Reader) io.Reader {
				return r
			})
			// wrap the io.Reader with sha256 hex hash reader, if x-amz-content-sha256
			// is the content sha256 - not a special payload type
			if !utils.IsSpecialPayload(hashPayload) {
				wrapBodyReader(ctx, func(r io.Reader) io.Reader {
					var cr io.Reader
					cr, err = utils.NewHashReader(r, hashPayload, utils.HashTypeSha256Hex)
					return cr
				})
				if err != nil {
					return err
				}
			}
			// wrap the io.Reader with ChunkReader if x-amz-content-sha256
			// provide chunk encoding value
			if utils.IsStreamingPayload(hashPayload) {
				wrapBodyReader(ctx, func(r io.Reader) io.Reader {
					var cr io.Reader
					cr, err = utils.NewChunkReader(ctx, r, authData, canonicalString, signingCred(account), tdate)
					return cr
				})
				if err != nil {
					return err
				}

				return nil
			}

			// Content-Length has to be set for data uploads: PutObject, UploadPart
			if contentLengthStr == "" {
				return s3err.GetAPIError(s3err.ErrMissingContentLength)
			}
			// the upload limit for big data actions: PutObject, UploadPart
			// is 5gb. If the size exceeds the limit, return 'EntityTooLarge' err
			if contentLength > utils.MaxObjSizeLimit {
				return s3err.GetEntityTooLargeErr(contentLength, utils.MaxObjSizeLimit)
			}

			return nil
		}

		if !utils.IsSpecialPayload(hashPayload) {
			// Calculate the hash of the request payload
			hashedPayload := sha256.Sum256(ctx.BodyRaw())
			hexPayload := hex.EncodeToString(hashedPayload[:])

			// Compare the calculated hash with the hash provided
			if hashPayload != hexPayload {
				return s3err.GetContentSHA256MismatchErr(hashPayload, hexPayload)
			}
		}

		return nil
	}
}

type accounts struct {
	root RootUserConfig
	iam  auth.IAMService
}

// RequestIAMService is an optional interface an auth.IAMService may
// implement when resolving an account needs the incoming request — e.g.
// when the backing store holds secrets externally and an authorizer
// derives a request-scoped signing key (auth.Account.SigningKey) from the
// request's credential scope. When implemented, it is used instead of
// GetUserAccount for request authentication lookups; the admin APIs and
// other non-request paths still use the base IAMService methods.
type RequestIAMService interface {
	GetUserAccountForRequest(ctx fiber.Ctx, access string) (auth.Account, error)
}

func (a accounts) getAccount(ctx fiber.Ctx, access string) (auth.Account, error) {
	if a.root.matches(access) {
		return auth.Account{
			Access: a.root.Access,
			Secret: a.root.Secret,
			Role:   auth.RoleAdmin,
		}, nil
	}

	if riam, ok := a.iam.(RequestIAMService); ok {
		return riam.GetUserAccountForRequest(ctx, access)
	}

	return a.iam.GetUserAccount(access)
}

// signingCred returns the account's credential material for signature
// verification: the pre-derived signing key when the account carries one,
// else the raw secret.
func signingCred(account auth.Account) utils.SigningCred {
	return utils.SigningCred{
		Secret:     account.Secret,
		SigningKey: account.SigningKey,
	}
}
