FROM golang
WORKDIR /concourse/resource-proxy
COPY . .
RUN go mod download
ENV CGO_ENABLED=0

WORKDIR /concourse/resource-proxy/check
RUN go build -o /assets/check

WORKDIR /concourse/resource-proxy/in
RUN go build -o /assets/in

WORKDIR /concourse/resource-proxy/out
RUN go build -o /assets/out

WORKDIR /concourse/resource-proxy/server
RUN go build -o /assets/server

FROM alpine
RUN apk --no-cache add bash ca-certificates tzdata
COPY --from=0 /assets /opt/resource
