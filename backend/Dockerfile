# Build a static binary, then run it on a minimal alpine image.
FROM golang:1.26 AS build
WORKDIR /src
COPY . .
# CGO_ENABLED=0 makes a fully static binary that runs on alpine (and scratch).
RUN CGO_ENABLED=0 GOOS=linux go build -o /app ./cmd/api

FROM alpine:3.20
# Run as a non-root user.
RUN adduser -D -u 10001 appuser
COPY --from=build /app /app
USER appuser
EXPOSE 8080
ENTRYPOINT ["/app"]
