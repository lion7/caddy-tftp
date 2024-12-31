# Caddy TFTP module

This is a Caddy App module that starts a TFTP server.

## Installation

Build with xcaddy:

```bash
xcaddy build --with=github.com/lion7/caddy-tftp
```

Optional: add capabilities to the binary to listen on privileged port 69:

```bash
sudo setcap 'cap_net_bind_service=+ep' ./caddy
```

## Configuration

Create a Caddy JSON configuration, in this example saved as `caddy.json`:

```json
{
  "apps": {
    "tftp": {
      "servers": {
        "": {
          "listen": ":69",
          "root": "/srv/tftp"
        }
      }
    }
  }
}
```

Note that by default this module will listen on port 69 and serve the current working directory as the root.
A minimal configuration:

```json
{
  "apps": {
    "tftp": {
      "servers": {
        "": {
        }
      }
    }
  }
}
```

## Running

Run the binary with the above config:

```bash
./caddy run --config caddy.json
```
