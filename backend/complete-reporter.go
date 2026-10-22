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
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/fil-forge/versitygw/s3response"
)

// CompleteMultipartUploadReporter is a Backend that can report a
// CompleteMultipartUpload's response headers before the completion
// finishes. With s3api.WithCompleteMultipartKeepalive, the response commits
// its headers at the report, as S3 commits its early 200 with
// x-amz-version-id and x-amz-server-side-encryption, and pads the body until
// the result. A Backend without it streams only a completion that outlasts
// the keepalive interval, and without those headers.
type CompleteMultipartUploadReporter interface {
	// CompleteMultipartUploadReportingHeaders is CompleteMultipartUpload
	// that reports the version id and encryption of the object it will
	// create before doing any slow work. The response sends its headers at
	// the report and nothing before it, so a backend that reports late
	// leaves its client without a keepalive, and an error after the report
	// can only go in the 200's body. It reports at most once. A report is
	// binding: a successful result must carry its version id, unless it
	// reported none, and its encryption. Returning without reporting is
	// allowed, and the response is then the usual one, with its real
	// status.
	CompleteMultipartUploadReportingHeaders(ctx context.Context, input *s3.CompleteMultipartUploadInput, report func(CompleteMultipartUploadHeaders)) (_ s3response.CompleteMultipartUploadResult, versionid string, _ error)
}

// CompleteMultipartUploadHeaders is what a CompleteMultipartUpload reports
// before its slow work: the headers of its response's early 200.
type CompleteMultipartUploadHeaders struct {
	// VersionID is the x-amz-version-id of the version the completion will
	// create, or "" to send the headers without one.
	VersionID string
	// ServerSideEncryption is the x-amz-server-side-encryption of the
	// object the completion will create, or "" for none.
	ServerSideEncryption types.ServerSideEncryption
}
