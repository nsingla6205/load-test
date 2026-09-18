# Debian 13 distroless nonroot (current tag includes libc6 2.41-12+deb13u4 / CVE-2026-5450).
FROM gcr.io/distroless/base:nonroot
# Define GOTRACEBACK to mark this container as using the Go language runtime
# for `skaffold debug` (https://skaffold.dev/docs/workflows/debug/).

ENV GOTRACEBACK=single
USER nonroot:nonroot
CMD ["/app"]
