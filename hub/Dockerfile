# Built for each platform by cross-compiling, so no emulation is needed:
#   docker buildx build --platform linux/amd64,linux/arm64 hub
FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS build
ARG TARGETOS TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o /fetch .

FROM scratch
COPY --from=build /fetch /fetch
ENV FETCH_DATA=/data
VOLUME /data
EXPOSE 8787
ENTRYPOINT ["/fetch", "serve"]
