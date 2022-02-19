# Concourse Resource Proxy

Implementing a [new resource type for Concourse](https://concourse-ci.org/implementing-resource-types.html) requires packaging the resource as container image and publishing it to a registry.

I wanted to iterate faster on a new resource, and came up with a proxy that forwards all requests to my local workstation, which hosts the resource under development. That way I can hack on the resource and run the changes as part of a real pipeline without having to push to a registry first.

# How to use it

Assuming that you want to hack on the [`concourse-time-resource`](https://github.com/concourse/time-resource):

1. Start the proxy server:

    ```command
    $ concourse-resource-proxy \
        --check concourse-time-resource/check/check \
        --in    concourse-time-resource/in/in \
        --out   concourse-time-resource/out/out
    ```

1. Now you can build the time resource locally:

    ```command
    $ git clone https://github.com/concourse/time-resource concourse-time-resource && cd concourse-time-resource
    $ (cd check && go build .)
    $ (cd in && go build .)
    $ (cd out && go build .)
    ```

1. Re-configure your pipeline to use the `concourse-resource-proxy`:

    ```yaml
    resource_types:
    - name: resource-proxy
      type: registry-image
      source:
        repository: suhlig/concourse-resource-proxy
        tag: latest

    resources:
    - name: every-hour-proxied
      source:
        url: https://example.com
        token: ((proxy-api-token))
      type: resource-proxy

    jobs:
    - name: announce
      plan:
      - get: every-hour-proxied
        trigger: true
    ```

1. Run a [manual resource check](https://concourse-ci.org/managing-resource-types.html):

    ```command
    $ fly -t example check-resource-type --resource-type my-pipeline/every-hour-proxied
    ```

  Both the proxy server (from step 1 above) and the `every-hour-proxied` resource will print the data going back and forth.

# Architecture

There are two components:

1. The `resource proxy` stands in for the resource under development, proxying all of Concourse's `{check, in, out}` requests to the
1. `proxy server`, which
   - receives the forwarded requests,
   - invokes the local `{check, in, out}` programs of the resource under development, and
   - returns their responses back to the resource proxy.

As a result, the resource under development can run on my local workstation and I can change it without having to push to a registry first.

# `resource proxy`

## Configuration

As the `resource proxy` is a regular Concourse resource type, it needs to be configured:

```yaml
- name: every-hour-proxied
  source:
    url: https://example.com
    token: ((proxy-api-token))
  type: resource-proxy
```

The `token` is used to protect the `proxy server`.

TODO Provide configuration to be passed to the resource under development

# Behavior

## `check`

Reads `STDIN` and forwards it to `((source.url))/check` (e.g. `https://example.com/check`). The response is written to `STDOUT` and `STDERR`.

## `in`

Reads `STDIN` and posts it to `((source.url))/check` (e.g. `https://example.com/check`). The response is written to `STDOUT` and `STDERR`.

Files created by the resource under development are copied into the output directory `$1`.

## `out`

TODO

# `proxy server`

## `/check`

Invokes `check` of the resource under development and passes the incoming stream of bytes as `STDIN`.

## `/in`

Invokes `in` of the resource under development and passes the incoming stream of bytes as `STDIN`. The name of a temporary directory is passed as `$1`. When `in` has finished, the contents of the temporary directory are returned as response to the caller.

## `/out`

TODO

# License

Same as [`gorilla/websocket`](https://github.com/gorilla/websocket), which major parts of this project are based on.
