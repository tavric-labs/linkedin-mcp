FROM --platform=$BUILDPLATFORM golang:1.27.2 AS build
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -trimpath -o /out/linkedin-mcp ./cmd/linkedin-mcp

FROM gcr.io/distroless/static-debian12
WORKDIR /app
COPY --from=build --chown=65532:65532 /out/linkedin-mcp /app/linkedin-mcp
EXPOSE 8080
USER 65532:65532
ENTRYPOINT ["/app/linkedin-mcp"]
