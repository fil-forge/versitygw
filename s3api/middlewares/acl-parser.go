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
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/fil-forge/versitygw/auth"
	"github.com/fil-forge/versitygw/backend"
	"github.com/fil-forge/versitygw/s3api/utils"
	"github.com/fil-forge/versitygw/s3err"
	"github.com/gofiber/fiber/v3"
)

// ParseAcl retreives the bucket acl and stores in the context locals
// if no bucket is found, it returns 'NoSuchBucket'
func ParseAcl(be backend.Backend) fiber.Handler {
	return func(ctx fiber.Ctx) error {
		bucket := ctx.Params("bucket")
		data, err := be.GetBucketAcl(ctx.RequestCtx(), &s3.GetBucketAclInput{Bucket: &bucket})
		if err != nil {
			return err
		}

		parsedAcl, err := auth.ParseACL(data)
		if err != nil {
			return err
		}

		// An ACL without a stored owner belongs to the root account. Without
		// a root account it has no owner: only admin-role accounts pass the
		// owner checks that follow, and no expected-bucket-owner can match.
		if parsedAcl.Owner == "" {
			if root, _ := utils.ContextKeyRootAccessKey.Get(ctx).(string); root != "" {
				parsedAcl.Owner = root
			}
		}

		// if expected bucket owner doesn't match the bucket owner
		// the gateway should return AccessDenied.
		// This header appears in all actions except 'CreateBucket' and 'ListBuckets'.
		// 'ParseACL' is also applied to all actions except for 'CreateBucket' and 'ListBuckets',
		// so it's a perfect place to check the expected bucket owner
		bucketOwner := ctx.Get("X-Amz-Expected-Bucket-Owner")
		if bucketOwner != "" && bucketOwner != parsedAcl.Owner {
			return s3err.GetAPIError(s3err.ErrAccessDenied)
		}

		utils.ContextKeyParsedAcl.Set(ctx, parsedAcl)
		return nil
	}
}
