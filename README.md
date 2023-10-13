# torpedo
Connect to databases in private VPCs the easy way - no VPN required

### How it works
On first run, `torpedo` will create an ECS cluster (this does not cost anything). From there, any time you want to connect to a database `torpedo` will generate random SSH keys, spin up a temporary container that has access to your database, and forward a local port through it. Then you can connect to the database on `localhost` as though it were running locally.

### Usage
Make sure you have a default AWS profile set or manually set one with `AWS_PROFILE=xxx torpedo`
```
NAME:
   torpedo - A tool to access AWS resources behind a VPC

USAGE:
   torpedo [global options] command [command options] [arguments...]

COMMANDS:
   client, c  Run the client command
   server, s  Run the server command
   help, h    Shows a list of commands or help for one command

GLOBAL OPTIONS:
   --verbose   (default: false)
   --help, -h  show help
```
### Acknowledgements

This wasn't my idea! There is a much more fleshed out product called `7777` here: https://port7777.com/ and the initial ideas [Matthieu Napoli](https://mnapoli.fr/) and [Marco Aurélio Deleu](https://blog.deleu.dev/). `torpedo` works in a similar way but was rewritten in Go for faster bootup speeds and made open source for community contribution.
