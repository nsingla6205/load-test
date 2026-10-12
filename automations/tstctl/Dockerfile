# Use the official Go base image
FROM golang:1.27.1-bookworm AS builder

# Set the working directory
WORKDIR /app

# Copy the Go executable into the container
COPY go.mod go.sum ./
RUN go mod download
COPY . .

RUN chmod +x run-tests.sh


# Build the Go application
RUN go mod tidy
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o tstctl .

FROM python:3.12-slim
WORKDIR /app

# Copy the compiled binary and script from the builder stage
COPY --from=builder /app/tstctl /app/tstctl
COPY --from=builder /app/run-tests.sh /app/run-tests.sh
RUN chmod +x /app/run-tests.sh
# Expose the port your server listens on
EXPOSE 8080

# Command to start the server (adjust if the binary invokes the script)
CMD ["/app/tstctl", "sanity"]