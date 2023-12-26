# multi-notify

A plugin forwarding messages to other WebHook servers for [gotify/server](https://github.com/gotify/server)
using [gotify/plugin-api](https://github.com/gotify/plugin-api).

## Getting Started

1. Clone this repository.
1. Build plugin with `make GOTIFY_VERSION="v2.4.0" build`.
1. Copy `build/multi-notifier-linux-amd64.so` to gotify server plugin directory.
1. Restart gotify server.
1. Configure this plugin in the gotify Web console.
1. Enable the plugin in the gotify Web console.

