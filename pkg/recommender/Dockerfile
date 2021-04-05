# Copyright 2017 The Kubernetes Authors. All rights reserved
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

FROM golang:1.15.8 as builder

WORKDIR /workspace
# Copy the Go Modules manifests
COPY . .

# Build
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GO111MODULE=on GOFLAGS=-mod=vendor go build -a -o recommender main.go

FROM gcr.io/distroless/static:latest
# MAINTAINER Krzysztof Grygiel "kgrygiel@google.com"

# ARG ARCH
# COPY recommender-$ARCH /recommender
COPY --from=builder /workspace/recommender .

ENTRYPOINT ["/recommender"]
CMD ["--v=4", "--stderrthreshold=info", "--prometheus-address=http://prometheus.monitoring.svc", "--dont-update-vpa-status=true"]
