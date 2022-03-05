# Concourse Resource Proxy

Implementing a [new resource type for Concourse](https://concourse-ci.org/implementing-resource-types.html) requires packaging the resource as container image and publishing it to a registry.

I wanted to iterate faster on a new resource, and came up with a proxy that forwards all requests to my local workstation, which hosts the resource under development. That way I can hack on the resource and run the changes as part of a real pipeline without having to push to a registry first.

# Caveats

* The runtime environment of the resource under development is quite different from Concourse - it runs side-by-side with the server (different OS and root file system; not running in a container).
* `STDERR` of the resource under development is not streamed back to Concourse. Instead, it directly prints to the resource server's `STDERR`.
* The exit code of the resource under development is not transferred to the resource proxy and thus does not show up in the Concourse UI.
* [Resource metadata](https://concourse-ci.org/implementing-resource-types.html#resource-metadata) is not implemented yet

# How to use it

Assuming that you want to hack on the [`concourse-time-resource`](https://github.com/concourse/time-resource) that is in `~/workspace/concourse-time-resource`:

1. Start the server:

    ```command
    $ concourse-resource-proxy-server \
        --addr localhost:8123 \
        --check ~/workspace/concourse-time-resource/check/check \
        --in    ~/workspace/concourse-time-resource/in/in \
        --out   ~/workspace/concourse-time-resource/out/out
    ```

    You can also run it in Docker:

    ```command
    # TODO mount concourse-time-resource directory
    # TODO expose port 8123
    $ docker run -it --rm suhlig/concourse-resource-proxy /opt/resource/server --addr localhost:8123 --check /concourse-time-resource/check
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
        url: wss://example.com
        token: ((proxy-api-token))
        proxied:
          interval: "30m" # this is passed to the resource under development as source
      type: resource-proxy

    jobs:
    - name: announce
      plan:
      - get: every-hour-proxied
        trigger: true
    ```

    The pipeline above is available as `example.yml` and can be set like this:

    ```command
    $ fly \
        --target "$CONCOURSE_TARGET" \
      set-pipeline \
        --pipeline resource-proxy-example \
        --config example.yml \
        --var proxy-api-token=s3cret
    ```

    Note that the `source.url` assumes that your local workstation is accessible via this address. You can use [ngrok](https://ngrok.com/) or similar services to forward a local port to a public URL. My personal solution is SSH remote port forwarding (`ssh -R 8123:localhost:8123 example.com`).

1. Run a [manual resource check](https://concourse-ci.org/managing-resource-types.html):

    ```command
    $ fly \
        --target "$CONCOURSE_TARGET" \
      check-resource-type \
        --resource-type resource-proxy-example/every-hour-proxied
    ```

  Both the server (from step 1 above) and the `every-hour-proxied` resource will print the data going back and forth.

# Architecture

There are two new components:

1. The `resource proxy` stands in for the resource under development, proxying all of Concourse's `{check, in, out}` requests to the
1. `resource server`, which
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
    url: wss://example.com
    token: ((proxy-api-token))
    proxied:
      interval: "30m" # this is passed to the resource under development as source
  type: resource-proxy
```

- `source.url` specifies where the server listens. The scheme _must_ be `ws` or `wss`. The proxy will append `/check`, `/in` or `/out` for the corresponding requests.
- `source.proxied` is passed to the resource under development as `source`
- `token` is used to protect the `server`

# Behavior

## `check`

Reads `STDIN` and forwards it to `((source.url))/check` (e.g. `https://example.com/check`). The response is written to `STDOUT` and `STDERR`.

![](doc/architecture-check.drawio.svg)

## `in`

Reads `STDIN` and forwards it to `((source.url))/check` (e.g. `https://example.com/check`). The response is written to `STDOUT` and `STDERR`.

Files created by the resource under development are copied into the output directory `$1`.

![](doc/architecture-in.drawio.svg)

## `out`

Reads `STDIN` and forwards it to `((source.url))/out` (e.g. `https://example.com/out`). The response is written to `STDOUT` and `STDERR`.

Files provided to the proxy at `$1` are copied and made available to the resource under development likewise.

![](doc/architecture-out.drawio.svg)

# `server`

## `/check`

Invokes `check` of the resource under development and passes the incoming stream of bytes as `STDIN`.

## `/in`

Invokes `in` of the resource under development and passes the incoming stream of bytes as `STDIN`. The name of a temporary directory is passed as `$1`. When `in` has finished, the contents of the temporary directory are returned as response to the caller.

## `/out`

Files received from the server are copied into a temporary directory. Then, `out` of the resource under development is invoked and the incoming stream of bytes as `STDIN` is passed.

# Release

There is a Concourse pipeline in `ci`. It creates a draft GitHub release for every tag:

```command
$ git tag -a v1.0.0-pre1 -m "Preparing release v1.0.0 - attempt 1"
$ git push --follow-tags
```

# CI

```command
$ fly \
    --target "$CONCOURSE_TARGET" \
  set-pipeline \
    --pipeline concourse-resource-proxy \
    --config ci/pipeline.yml \
    --load-vars-from ci/private-config.yml
```

# Development

* `scripts/test-*` manually invokes a local copy of Concourse' time resource via proxy
* `scripts/iterate` restarts the server when go files were changed

# License

Same as [`gorilla/websocket`](https://github.com/gorilla/websocket), which parts of this project are based on.
