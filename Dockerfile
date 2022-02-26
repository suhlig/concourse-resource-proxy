FROM golang
WORKDIR /concourse/resource-proxy
COPY . .
RUN go mod download
ENV CGO_ENABLED=0

ENV GOBIN=/assets
RUN go install ./...

FROM alpine
RUN apk --no-cache add bash ca-certificates tzdata
COPY --from=0 /assets /opt/resource
