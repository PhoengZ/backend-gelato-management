# Infrastructure Documentation

This directory contains the core infrastructure and configuration files required to run the Backend Gelato Management system, including Docker containers, databases, and message brokers (RabbitMQ).

## 📁 Folder Structure

```text
infra/
├── .env
├── .env.example
├── docker/
│   ├── compose.dev.yml
│   └── compose.yml
└── rabbitmq/
    ├── definitions.json
    ├── definitions_example.json
    ├── enabled_plugins
    └── rabbitmq.conf
```

## 📄 File Roles & Responsibilities

### Root Directory
- **`.env` / `.env.example`**: Contains environment variables required by the infrastructure (e.g., default user credentials, secret keys).

### Docker (`docker/`)
- **`compose.yml`**: The primary Docker Compose file. It defines the production or default stack of services including `rabbitmq`, `mongodb`, and `analytics-service`.
- **`compose.dev.yml`**: Docker Compose configuration specifically used to override or add services for a local development environment.

### RabbitMQ (`rabbitmq/`)
- **`definitions.json`**: The actual pre-loaded state of the RabbitMQ server. It defines the active users (with hashed passwords), vhosts, permissions, queues, and exchanges loaded into the broker upon startup.
- **`definitions_example.json`**: A template file serving as a reference on how to structure `definitions.json`.
- **`enabled_plugins`**: A text list of RabbitMQ plugins to be enabled on boot (e.g., `rabbitmq_management`).
- **`rabbitmq.conf`**: The main configuration file for RabbitMQ controlling networking, resource limits, and health-check timeouts.

---

## 🐇 How to Initiate a RabbitMQ URI

When creating a new service that needs to connect to RabbitMQ, you will need a standard AMQP connection string (URI) formatted as:
`amqp://<user>:<password>@<host>:<port>/<vhost>`

To provision these credentials, you must update the `infra/rabbitmq/definitions.json` file. You can use `infra/rabbitmq/definitions_example.json` as a guide.

### Step-by-Step Configuration:

**1. Define a Virtual Host (vhost)**
Add your new service's vhost to the `vhosts` array:
```json
"vhosts": [
  { "name": "/" },
  { "name": "your_new_vhost" }
]
```

**2. Create a User**
Add a user for your service into the `users` array. Note that passwords must be hashed securely using `rabbit_password_hashing_sha256`. 
```json
"users": [
  {
    "name": "your_service_user",
    "password_hash": "base64_encoded_sha256_hash",
    "hashing_algorithm": "rabbit_password_hashing_sha256",
    "tags": []
  }
]
```

**3. Assign Permissions**
Map the user to the vhost in the `permissions` array, granting the necessary configuration, read, and write privileges via Regex patterns:
```json
"permissions": [
  {
    "user": "your_service_user",
    "vhost": "your_new_vhost",
    "configure": ".*",
    "write": ".*",
    "read": ".*"
  }
]
```

**4. Formulate the Connection String**
Once the container restarts and definitions are applied, the resulting URI you would place in your service's environment variables would be:
`amqp://your_service_user:actual_unhashed_password@rabbitmq:5672/your_new_vhost`

> **Note:** The host `rabbitmq` is used because the service communicates internally via the Docker network `gelato_network`.
