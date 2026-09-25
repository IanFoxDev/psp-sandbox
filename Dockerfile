# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build
ARG TARGETOS TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/psp-sandbox ./cmd/psp-sandbox

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/psp-sandbox /psp-sandbox
EXPOSE 8090
USER nonroot:nonroot
ENTRYPOINT ["/psp-sandbox"]
