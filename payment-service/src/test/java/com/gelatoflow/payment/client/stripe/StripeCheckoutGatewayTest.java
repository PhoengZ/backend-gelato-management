package com.gelatoflow.payment.client.stripe;

import static com.gelatoflow.payment.testsupport.CheckoutFixtures.*;
import static org.assertj.core.api.Assertions.*;

import com.gelatoflow.contracts.payment.v1.CreateCheckoutSessionRequest;
import com.gelatoflow.payment.service.CheckoutService;
import com.gelatoflow.payment.testsupport.Fixtures;
import com.stripe.StripeClient;
import com.sun.net.httpserver.HttpServer;
import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import java.net.InetSocketAddress;
import java.net.URLDecoder;
import java.nio.charset.StandardCharsets;
import java.util.*;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.atomic.AtomicReference;
import org.junit.jupiter.api.*;

class StripeCheckoutGatewayTest {
  HttpServer server;
  StripeClient client;
  final Map<String, String> bodies = new ConcurrentHashMap<>();
  final AtomicReference<String> state = new AtomicReference<>("open");
  final AtomicReference<String> mode = new AtomicReference<>("");
  CreateCheckoutSessionRequest request;

  @BeforeEach
  void setup() throws Exception {
    request = request();
    server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
    server.createContext(
        "/v1/checkout/sessions",
        exchange -> {
          int status = 200;
          String response;
          if ("POST".equals(exchange.getRequestMethod())) {
            var body = new String(exchange.getRequestBody().readAllBytes(), StandardCharsets.UTF_8);
            var key = exchange.getRequestHeaders().getFirst("Idempotency-Key");
            var previous = bodies.putIfAbsent(key, body);
            if (previous != null && !previous.equals(body)) {
              status = 400;
              response =
                  "{\"error\":{\"type\":\"idempotency_error\",\"message\":\"private provider details\"}}";
            } else if ("unavailable".equals(mode.get())) {
              status = 503;
              response =
                  "{\"error\":{\"type\":\"api_error\",\"message\":\"private provider details\"}}";
            } else if ("concurrent".equals(mode.get())) {
              status = 409;
              response =
                  "{\"error\":{\"type\":\"invalid_request_error\",\"code\":\"idempotency_key_in_use\",\"message\":\"busy\"}}";
            } else {
              response = sessionJson("open"); // Cached create snapshot deliberately remains open.
            }
          } else {
            response = sessionJson(state.get());
          }
          var bytes = response.getBytes(StandardCharsets.UTF_8);
          exchange.getResponseHeaders().add("Content-Type", "application/json");
          exchange.sendResponseHeaders(status, bytes.length);
          try (var stream = exchange.getResponseBody()) {
            stream.write(bytes);
          }
        });
    server.start();
    client =
        StripeClient.builder()
            .setApiKey("sk_test_fixture")
            .setApiBase("http://127.0.0.1:" + server.getAddress().getPort())
            .build();
  }

  String sessionJson(String status) {
    var metadata = StripeCheckoutGateway.metadata(request);
    var json = new java.util.LinkedHashMap<String, Object>();
    json.put("mode", "payment");
    json.put("payment_method_types", List.of("promptpay"));
    json.put("payment_status", "unpaid");
    json.putAll(
        Map.of(
            "id",
            "cs_test_fixture",
            "object",
            "checkout.session",
            "url",
            URL,
            "status",
            status,
            "livemode",
            false,
            "amount_total",
            25000,
            "currency",
            "thb",
            "client_reference_id",
            request.getOrderId(),
            "metadata",
            metadata));
    return new com.google.gson.Gson().toJson(json);
  }

  @AfterEach
  void cleanup() {
    server.stop(0);
  }

  CheckoutService service() {
    return new CheckoutService(
        new StripeCheckoutGateway(client, checkoutProperties()), Fixtures.properties());
  }

  @Test
  void replayAcrossAdapterInstancesReturnsSameSessionAndStableImmutableProviderParameters() {
    var first = service().create(request);
    var second = service().create(request);
    assertThat(second).isEqualTo(first);
    var body =
        URLDecoder.decode(bodies.get("gelatoflow-checkout-v1-" + KEY), StandardCharsets.UTF_8);
    assertThat(body)
        .contains(
            "mode=payment",
            "line_items[0][quantity]=1",
            "line_items[0][price_data][unit_amount]=25000",
            "line_items[0][price_data][currency]=thb",
            "metadata[order_id]=" + request.getOrderId(),
            "metadata[checkout_key]=" + KEY,
            "payment_intent_data[metadata][customer_id]=" + request.getCustomerId(),
            "allowed_payment_method_types[0]=promptpay",
            "success_url=" + checkoutProperties().successUrl());
    assertThat(body).doesNotContain("expires_at=", "=card", "allowed_payment_method_types[1]");
    assertThat(bodies).hasSize(1);
  }

  @Test
  void concurrentAdaptersUseTheSameProviderKeyAndSession() throws Exception {
    try (var executor = java.util.concurrent.Executors.newFixedThreadPool(2)) {
      var start = new java.util.concurrent.CountDownLatch(1);
      var first =
          executor.submit(
              () -> {
                start.await();
                return service().create(request);
              });
      var second =
          executor.submit(
              () -> {
                start.await();
                return service().create(request);
              });
      start.countDown();
      assertThat(first.get(5, java.util.concurrent.TimeUnit.SECONDS))
          .isEqualTo(second.get(5, java.util.concurrent.TimeUnit.SECONDS));
      assertThat(bodies).hasSize(1);
    }
  }

  @Test
  void sameKeyWithChangedAmountCustomerOrderOrDeadlineIsAlreadyExists() {
    service().create(request);
    for (var changed :
        new CreateCheckoutSessionRequest[] {
          request.toBuilder().setAmountMinor(25001).build(),
          request.toBuilder().setCustomerId(Fixtures.OTHER).build(),
          request.toBuilder().setOrderId(Fixtures.OTHER).build(),
          request.toBuilder()
              .setReservationExpiresAt(
                  request.getReservationExpiresAt().toBuilder()
                      .setSeconds(request.getReservationExpiresAt().getSeconds() + 1))
              .build()
        }) {
      assertThatThrownBy(() -> service().create(changed))
          .isInstanceOfSatisfying(
              StatusRuntimeException.class,
              error ->
                  assertThat(error.getStatus().getCode()).isEqualTo(Status.Code.ALREADY_EXISTS));
    }
  }

  @Test
  void replayReconcilesCurrentStateRatherThanReturningCachedOpenSession() {
    service().create(request);
    state.set("expired");
    assertThatThrownBy(() -> service().create(request))
        .isInstanceOfSatisfying(
            StatusRuntimeException.class,
            error ->
                assertThat(error.getStatus().getCode()).isEqualTo(Status.Code.FAILED_PRECONDITION));
  }

  @Test
  void transientProviderErrorAndConcurrentKeyUseRemainRetryableWithoutLeakingDetails() {
    for (var scenario :
        Map.of("unavailable", Status.Code.UNAVAILABLE, "concurrent", Status.Code.ABORTED)
            .entrySet()) {
      mode.set(scenario.getKey());
      assertThatThrownBy(() -> service().create(request))
          .isInstanceOfSatisfying(
              StatusRuntimeException.class,
              error -> {
                assertThat(error.getStatus().getCode()).isEqualTo(scenario.getValue());
                assertThat(error.getMessage()).doesNotContain("private provider details");
              });
    }
  }
}
