# Build a static binary, then run it on a minimal alpine image.
FROM golang:1.26 AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /app ./cmd/worker

FROM alpine:3.20
RUN adduser -D -u 10001 appuser
COPY --from=build /app /app
USER appuser
ENTRYPOINT ["/app"]
