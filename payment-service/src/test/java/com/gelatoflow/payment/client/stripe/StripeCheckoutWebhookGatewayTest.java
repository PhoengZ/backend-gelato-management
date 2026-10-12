package com.gelatoflow.payment.client.stripe;

import static org.assertj.core.api.Assertions.*;

import com.gelatoflow.payment.exception.ApiException;
import com.stripe.StripeClient;
import com.sun.net.httpserver.HttpServer;
import java.net.InetSocketAddress;
import java.net.URLDecoder;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicReference;
import org.junit.jupiter.api.*;

class StripeCheckoutWebhookGatewayTest {
  HttpServer server;
  StripeCheckoutWebhookGateway gateway;
  AtomicInteger status = new AtomicInteger(200);
  AtomicInteger calls = new AtomicInteger();
  AtomicReference<String> query = new AtomicReference<>();

  @BeforeEach
  void setup() throws Exception {
    server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
    server.createContext(
        "/v1/checkout/sessions/cs_test_fixture",
        exchange -> {
          calls.incrementAndGet();
          query.set(
              exchange.getRequestMethod()
                  + " "
                  + URLDecoder.decode(
                      exchange.getRequestURI().getRawQuery(), StandardCharsets.UTF_8));
          var response =
              status.get() == 200
                  ? "{\"id\":\"cs_test_fixture\",\"object\":\"checkout.session\",\"payment_intent\":{\"id\":\"pi_fixture\",\"object\":\"payment_intent\",\"status\":\"succeeded\"}}"
                  : "{\"error\":{\"type\":\"api_error\",\"message\":\"private stripe details\"}}";
          var bytes = response.getBytes(StandardCharsets.UTF_8);
          exchange.getResponseHeaders().set("Content-Type", "application/json");
          exchange.sendResponseHeaders(status.get(), bytes.length);
          try (var body = exchange.getResponseBody()) {
            body.write(bytes);
          }
        });
    server.start();
    gateway =
        new StripeCheckoutWebhookGateway(
            StripeClient.builder()
                .setApiKey("sk_test_fixture")
                .setApiBase("http://127.0.0.1:" + server.getAddress().getPort())
                .build());
  }

  @AfterEach
  void cleanup() {
    server.stop(0);
  }

  @Test
  void retrievesFreshSessionWithExpandedPaymentIntentUsingTheSdk() {
    var session = gateway.retrieve("cs_test_fixture");
    assertThat(session.getPaymentIntentObject().getStatus()).isEqualTo("succeeded");
    assertThat(query.get()).startsWith("GET ").contains("payment_intent", "expand");
  }

  @Test
  void providerFailureIsSanitizedAndDoesNotAutomaticallyRetry() {
    status.set(503);
    assertThatThrownBy(() -> gateway.retrieve("cs_test_fixture"))
        .isInstanceOfSatisfying(
            ApiException.class,
            e -> {
              assertThat(e.code()).isEqualTo("PAYMENT_PROVIDER_ERROR");
              assertThat(e.getMessage())
                  .doesNotContain("private stripe details", "sk_test_fixture");
            });
    assertThat(calls.get()).isEqualTo(1);
  }
}
