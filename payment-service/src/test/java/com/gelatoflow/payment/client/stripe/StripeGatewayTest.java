package com.gelatoflow.payment.client.stripe;

import static com.gelatoflow.payment.testsupport.Fixtures.*;
import static org.assertj.core.api.Assertions.*;

import com.stripe.StripeClient;
import com.sun.net.httpserver.HttpServer;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.atomic.AtomicReference;
import org.junit.jupiter.api.Test;

class StripeGatewayTest {
  @Test
  void sdkUsesAuthoritativeMinorUnitsMetadataAndStableProviderIdempotencyKey() throws Exception {
    var key = new AtomicReference<String>();
    var auth = new AtomicReference<String>();
    var form = new AtomicReference<String>();
    var server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
    server.createContext(
        "/v1/payment_intents",
        exchange -> {
          key.set(exchange.getRequestHeaders().getFirst("Idempotency-Key"));
          auth.set(exchange.getRequestHeaders().getFirst("Authorization"));
          form.set(new String(exchange.getRequestBody().readAllBytes(), StandardCharsets.UTF_8));
          var body =
              "{\"id\":\"pi_fixture\",\"object\":\"payment_intent\",\"client_secret\":\"pi_fixture_secret_fixture\"}"
                  .getBytes(StandardCharsets.UTF_8);
          exchange.getResponseHeaders().add("Content-Type", "application/json");
          exchange.sendResponseHeaders(200, body.length);
          try (var output = exchange.getResponseBody()) {
            output.write(body);
          }
        });
    server.start();
    try {
      var client =
          StripeClient.builder()
              .setApiKey("sk_test_fixture")
              .setApiBase("http://127.0.0.1:" + server.getAddress().getPort())
              .build();
      var gateway = new StripeGateway(client);
      assertThat(gateway.create(ORDER, CUSTOMER, 25000, "THB").getId()).isEqualTo("pi_fixture");
      assertThat(key.get()).isEqualTo("gelatoflow-payment-v1-" + ORDER);
      assertThat(auth.get()).isEqualTo("Bearer sk_test_fixture");
      var decoded = java.net.URLDecoder.decode(form.get(), StandardCharsets.UTF_8);
      assertThat(decoded)
          .contains(
              "amount=25000",
              "currency=thb",
              "metadata[order_id]=" + ORDER,
              "metadata[customer_id]=" + CUSTOMER,
              "metadata[integration]=gelatoflow-v1",
              "allowed_payment_method_types[0]=promptpay")
          .doesNotContain("=card", "allowed_payment_method_types[1]");
      gateway.create(ORDER, CUSTOMER, 25000, "THB");
      assertThat(key.get()).isEqualTo("gelatoflow-payment-v1-" + ORDER);
    } finally {
      server.stop(0);
    }
  }
}
