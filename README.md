# accept-banano

Payment gateway for [BANANO](https://banano.cc)

*accept-banano* is a server program that helps you to accept BANANO payments in a fast, secure and cost-efficient way.

You can use it independently or together with it's web client [accept-banano-client](https://github.com/accept-nano/accept-nano-client).

## Installing

There are several options:
 - Download the latest binary from [releases page](https://github.com/tigwyk/accept-banano/releases)
 - Pull Docker image: `docker pull tigwyk/acceptbanano`
 - Compile from source: `go get -u github.com/tigwyk/accept-banano`

## Running

 - You need a running BANANO node (version >= 21) for communicating with BANANO network.
   - If you are going to setup your own, see [instructions](https://docs.nano.org/running-a-node/node-setup/)
     - `"rpc_enable"` and `"enable_control"` options must be enabled in [node config](https://docs.nano.org/running-a-node/configuration/)
   - If you don't want to setup your own, you can use a node proxy. There are several options. Some of them are:
     - https://mynano.ninja/api/node
     - https://api.nanos.cc/
 - Create a config file for *accept-banano*. See [Config section](#config) below.
 - Run command: `accept-banano -config /path/to/the/config.toml`

## Docker

You can create a Docker container for accept-banano that works perfectly with your [Docker Nano Node](https://docs.nano.org/running-a-node/docker-management/).
The configuration and database are stored at `/opt/data` so you should map that folder to your host.

#### Standalone

    docker run -d -p 8080:8080 -v ~/accept-banano:/opt/data tigwyk/acceptbanano

#### Docker Compose

Example configuration with BANANO node:

```yaml
version: '3'
services:
  accept-nano:
    image: "tigwyk/acceptbanano"
    restart: "unless-stopped"
    ports:
     - "8080:8080"
    volumes:
     - "~/accept-banano:/opt/data"
  node:
    image: "bananocoin/banano"
    restart: "unless-stopped"
    ports:
     - "7071:7071/udp"
     - "7071:7071"
     - ":::7072:7072"
     - ":::7072:7074"
    volumes:
     - "~:/root"
```

## How it works?

 - *accept-banano* is a HTTP server with 2 primary endpoints.
   - **/api/pay** for creating a payment request.
   - **/api/verify** for checking the status of a payment.
 - From client, you create a payment request by posting the currency and amount.
 - When *accept-banano* receives a payment request, it creates a random unique address for the payment and saves it in its database, then returns a unique token to the client.
 - After the payment is created, *accept-nano* starts monitoring the destination account for incoming funds. It does this by sending a request to node and listening blocks from network via Websocket connection.
 - While *accept-banano* is checking the payment, the client also checks by calling the verification endpoint. It does this continuously until the payment is verified.
 - The customer has a limited amount of time to transfer the funds to the destination account. This duration can be set in *accept-banano* config.
 - Then the customer pays the requested amount.
 - If *accept-banano* sees a pending block at destination account, it sends a notification to the merchant and changes the status of the payment to "verified".
 - At this point, the payment is received and the merchant is notified. The client can continue its flow.
 - The server accepts pending blocks at the destination account.
 - The server sends the funds in destination account to the merchants account defined in the config file.

## Config

 - Config is written in TOML or YAML format.
 - The structure of config file is defined in [config.go](https://github.com/accept-nano/accept-nano/blob/master/config.go). See comments for field descriptions.
 - All of the configuration options can be overriden with `ACCEPTBANANO_` prefixed environment variables. This makes configuring the Docker container easier.

### Example config.toml

```toml
DatabasePath = "./accept-banano.db"
ListenAddress = "127.0.0.1:8080"
NodeURL = "http://localhost:7072/"
# Don't forget to set your merchant account.
Account = "ban_1youraccount3fp9utkor5ixmxyg8kme8fnzc4zty145ibch8kf5jwpnzr3r"
# Generate a new random seed with "accept-nano -seed" command and keep it secret.
Seed = "12F36345AB0B10557F22B36B5FF241EF09AF7AEA00A40B3F52CCD34640040E92"
# Payment notifications will be sent to this URL (optional).
NotificationURL = "http://localhost:5000/"
# CoinMarketCap API key. Available from https://coinmarketcap.com/api/
CoinmarketcapAPIKey = "123ab456-cd78-90ef-ab12-34cd56ef7890"
```

## Security

 - *accept-banano* does not need to know your merchant wallet seed. It takes payments from customers and sends them to your merchant account address defined in config file.
 - *accept-banano* server is designed to be open to the Internet but you can run it in your internal network and control requests to it if you want to be extra safe.
 - *accept-banano* does not keep funds itself and passes incoming payments to the merchant account immediately. So there is only a short period of time when the funds are held by *accept-banano*.
 - Private keys are not saved in the database and derived from the seed defined in the config. So you are safe even if the database file is stolen.
 - Key generation and block signing is done in *accept-nano* process. That means private keys does not leave the process in any circumstances.

## Contributing

 - Please open an issue if you have a question or suggestion.
 - Don't create a PR before discussing it first.

## Who is using *accept-banano* in production?

 - Nobody, yet

Please send a PR to list your site if *accept-banano* is helping you to receive BANANO payments.
