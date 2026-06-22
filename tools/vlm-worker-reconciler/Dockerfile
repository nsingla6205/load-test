FROM --platform=$BUILDPLATFORM mirror.gcr.io/library/golang:1.25.9-alpine AS build
ARG TARGETARCH
WORKDIR /workspace
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -o /reconciler ./tools/vlm-worker-reconciler/src

FROM gcr.io/distroless/static:nonroot
COPY --from=build /reconciler /reconciler
USER nonroot:nonroot
ENTRYPOINT ["/reconciler"]
