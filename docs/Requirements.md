# Exchange Rate Service

Implement a currency exchange rate quotation service.

The service must provide an asynchronous interface. This means that a user first submits a request to update an exchange rate and then, after some time, submits a request to retrieve the updated rate. The actual exchange rate update must be performed in the background.

## Requirements

The service must be written in Go. It must provide an HTTP API using JSON for communication.

The API must support the following operations:

**1. Update an exchange rate.**

The request specifies a currency pair (e.g., EUR/MXN).

The service assigns a unique identifier to the update request and returns this identifier in the response.

The service performs the exchange rate update in the background. In other words, the request handler must not execute the update itself.

**2. Retrieve an exchange rate by update ID.**

The request specifies the update ID (from operation 1). The response contains the exchange rate value and the time of the update.

**3. Retrieve the latest exchange rate.**

The request specifies a currency code. The response contains the exchange rate value and the time of the update.

The service's database in built on PostgreSQL and deployed in Docker.

Background exchange rate updates can be implemented using any approach chosen by the developer, such as periodic polling (cron), event-driven processing (e.g., using channels), or other methods.

The service retrieves exchange rate values from an external data source exchangerate.dev.
exchangerate.dev
Service support only USD, EUR, MXN rates. Supporting all possible currencies is not required.

Do not forget to add:

- Unit tests
- Containerization (Docker)
- Idempotency of exchange rate updates
- Swagger (OpenAPI) API specification fileexchangerate.dev.exchangerate.dev.