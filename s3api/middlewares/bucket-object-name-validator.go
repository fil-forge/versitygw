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
	"github.com/fil-forge/versitygw/s3api/utils"
	"github.com/fil-forge/versitygw/s3err"
	"github.com/gofiber/fiber/v3"
)

// BucketObjectNameValidator extracts and validates the bucket and object names
// from the request URI. checkTraversal enables the path-traversal (non-local
// key) rejection, which suits filesystem-backed backends; backends that store
// keys opaquely pass false so keys like "../file.txt" are accepted literally.
func BucketObjectNameValidator(checkTraversal bool) fiber.Handler {
	return func(ctx fiber.Ctx) error {
		bucket, object := parsePath(ctx.Path())

		// check if the provided bucket name is valid
		if !utils.IsValidBucketName(bucket) {
			return s3err.GetBucketErr(s3err.ErrInvalidBucketName, bucket)
		}

		// check if the provided object name is valid
		// skip for empty objects: e.g bucket operations: HeadBucket...
		if object != "" {
			// A C1 control character in the key is unparseable to S3: reject
			// pre-auth with InvalidURI, matching AWS. C0 controls (CR/LF/ESC)
			// are legal and handled by the general validity check below.
			if utils.ContainsC1ControlChar(object) {
				return s3err.GetAPIError(s3err.ErrInvalidURI)
			}
			if !utils.IsObjectNameValidWithTraversal(object, checkTraversal) {
				return s3err.GetAPIError(s3err.ErrBadRequest)
			}
		}

		return nil
	}
}
