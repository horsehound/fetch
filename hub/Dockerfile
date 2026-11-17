FROM golang:1.24-alpine AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /fetch .

FROM scratch
COPY --from=build /fetch /fetch
ENV FETCH_DATA=/data
VOLUME /data
EXPOSE 8787
ENTRYPOINT ["/fetch", "serve"]
