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

package backend

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/fil-forge/versitygw/s3response"
)

// CompleteMultipartUploadReporter is a Backend that can report a
// CompleteMultipartUpload's version id before the completion finishes. With
// s3api.WithCompleteMultipartKeepalive, the response commits its headers at
// the report, as S3 commits its early 200 with x-amz-version-id, and pads the
// body until the result. A Backend without it streams only a completion that
// outlasts the keepalive interval, and without the header.
type CompleteMultipartUploadReporter interface {
	// CompleteMultipartUploadReportingVersionID is CompleteMultipartUpload
	// that reports the version id it will create before doing any slow
	// work: reportVersionID(id), or reportVersionID("") to send the headers
	// without one. The response sends its headers at the report and nothing
	// before it, so a backend that reports late leaves its client without a
	// keepalive, and an error after the report can only go in the 200's
	// body. It reports at most once. A reported id is binding: a successful
	// result must carry it. Returning without reporting is allowed, and the
	// response is then the usual one, with its real status.
	CompleteMultipartUploadReportingVersionID(ctx context.Context, input *s3.CompleteMultipartUploadInput, reportVersionID func(versionID string)) (_ s3response.CompleteMultipartUploadResult, versionid string, _ error)
}
