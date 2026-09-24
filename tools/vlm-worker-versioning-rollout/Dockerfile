FROM --platform=$BUILDPLATFORM mirror.gcr.io/library/golang:1.25.12-alpine AS build
ARG TARGETARCH
WORKDIR /workspace
COPY . .
ENV GOEXPERIMENT=boringcrypto
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -o /rollout ./tools/vlm-worker-versioning-rollout

FROM gcr.io/distroless/static:nonroot
COPY --from=build /rollout /rollout
USER nonroot:nonroot
ENTRYPOINT ["/rollout"]
