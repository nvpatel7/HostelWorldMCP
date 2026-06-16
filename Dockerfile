# Build a static binary, then ship it in a tiny distroless image.
FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /hostelworld-mcp ./cmd/hostelworld-mcp

FROM gcr.io/distroless/static-debian12:nonroot
# distroless ships CA certs, needed for outbound HTTPS to hostelworld.com.
COPY --from=build /hostelworld-mcp /hostelworld-mcp
# Railway injects $PORT; the app binds 0.0.0.0:$PORT automatically (see config).
ENTRYPOINT ["/hostelworld-mcp"]
