# Concourse Resource Proxy

Implementing a [new resource type for Concourse](https://concourse-ci.org/implementing-resource-types.html) requires packaging the resource as registry image and publishing it to a registry.

I wanted to iterate faster on a new resource, and came up with a proxy resource type that forwards all requests to my local workstation, which hosts the resource under development.

There are two components:

1. The `resource proxy` stands in for the resource under development, proxying all of Concourse's `{check, in, out}` requests via http.
1. A `http server` receives the forwarded requests, invokes the local `{check, in, out}` scripts of the resource under development, and returns their responses back to the proxy.

As a result, the resource under development can run on my local workstation and I can change it without having to push to a registry first.

# `resource proxy`

## Configuration

As the `resource proxy` is a regular Concourse resource type, it needs to be configured:

```yaml
name: database
source:
  url: https://example.com
  token: ((proxy-api-token))
type: resource-proxy
```

The `token` is used to protect the `http server`.

TODO Provide configuration to be passed to the resource under development

## `check` behavior

Reads `STDIN` and forwards it to `((source.url))/check` (e.g. `https://example.com/check`). The response is written to `STDOUT` and `STDERR`.

## `in` behavior

Reads `STDIN` and posts it to `((source.url))/check` (e.g. `https://example.com/check`). The response is written to `STDOUT` and `STDERR`.

Files created by the resource under development are passed on to the output directory.

## `out` behavior

TODO

# `http server`

## `/check` behavior

Invokes `check` of the resource under development and passes the incoming stream of bytes as `STDIN`.

## `/in` behavior

Invokes `in` of the resource under development and passes the incoming stream of bytes as `STDIN`. The name of a temporary directory is passed as `$1`. When `in` has finished, the contents of the temporary directory are returned as response to the caller.

## `/out` behavior

TODO

# License

Same as [`gorilla/websocket`](https://github.com/gorilla/websocket), which major parts of this project are based on.
