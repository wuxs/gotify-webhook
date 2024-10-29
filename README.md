# gotify-webhook

A plugin forwarding messages to Webhook servers for [gotify/server](https://github.com/gotify/server)
using [gotify/plugin-api](https://github.com/gotify/plugin-api).

## Getting Started

1. Clone this repository.
2. Download `gomod-cap` with `make download-tools` 
1. Build plugin with `make GOTIFY_VERSION="v2.5.0" build`.
1. Copy `build/webhook-linux-amd64.so` to the Gotify server plugin directory.
1. Restart the Gotify server.
1. Configure this plugin in the Gotify web console.
1. Enable the plugin in the Gotify web console.

## Configuration Guide

### Webhook

You can configure multiple webhooks to which messages can be forwarded to.

A full example is as follows:

```yaml
- url: http://pool:5678/webhook-test/mqtt
  apps:
    - 1
    - 7
    - 4
  method: POST
  header:
    Content-Type: application/json
  body: "{{.title}}\n\n{{.message}}"
```

#### Field specification

| Field  | Sub-field    | Type            | Required | Default    | Description             |
| ---    | ---          | ---             | ---      | ---        | ---                     |
| url    |              | URL             | Y        |            | Webhook URL             |
| apps   |              | Array           | N        |            | Gotify application IDs. |
| method |              | String          | N        | POST       | HTTP request method.    |
| header |              | Key-value pairs | N        |            | HTTP request headers.   |
|        | Content-Type | String          | N        | text/plain |                         |
| body   |              | String          | N        |            | HTTP request body.      |

##### Application ID

When configured, only messages from these applications can be forwarded to this webhook. Otherwise, 
every message is forwarded.

Application IDs can be found by sending a request to the Gotify REST-API, which is delivered along
with your Gotify server. You can visit it through the relative path `/docs`, for example, if your
Gotify's URL is `http://pool:9090/`, then you can visit the REST-API through `http://pool:9090/docs`.

##### Body

The body field can either be a plain string or a template. As the latter, the following placeholders
are supported:

- `{{.title}}`: Title of the forwarded message.
- `{{.message}}`: Content of the forwarded message.
