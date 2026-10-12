package com.gelatoflow.payment.client.order;

import com.gelatoflow.payment.config.OrderCallbackProperties;
import com.gelatoflow.payment.exception.ApiException;
import com.gelatoflow.payment.validation.Ids;
import jakarta.annotation.PreDestroy;
import java.io.IOException;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.time.Duration;
import java.util.UUID;
import org.springframework.http.HttpStatus;
import org.springframework.stereotype.Component;

@Component
public class HttpOrderCallbackClient implements OrderCallbackPort, AutoCloseable {
  private final OrderCallbackProperties properties;
  private final HttpClient client;

  public HttpOrderCallbackClient(OrderCallbackProperties properties) {
    this.properties = properties;
    client =
        HttpClient.newBuilder()
            .connectTimeout(Duration.ofMillis(properties.timeoutMs()))
            .followRedirects(HttpClient.Redirect.NEVER)
            .build();
  }

  @Override
  public void succeeded(String orderId) {
    send(orderId, "payment-succeeded");
  }

  @Override
  public void failed(String orderId) {
    send(orderId, "payment-failed");
  }

  private void send(String orderId, String action) {
    if (!properties.enabled() || !Ids.isUuid(orderId)) throw unavailable();
    var origin = URI.create(properties.baseUrl());
    var path = "/api/v1/orders/" + UUID.fromString(orderId) + "/" + action;
    var request =
        HttpRequest.newBuilder(origin.resolve(path))
            .timeout(Duration.ofMillis(properties.timeoutMs()))
            .header("X-Payment-Service-Token", properties.serviceToken())
            .header("Accept", "application/json")
            .POST(HttpRequest.BodyPublishers.noBody())
            .build();
    try {
      var response = client.send(request, HttpResponse.BodyHandlers.ofInputStream());
      // Only status is part of this contract. Close without draining an unbounded
      // error body; the request deadline bounds waiting for response headers.
      try (var body = response.body()) {
        if (response.statusCode() == 409) {
          throw new ApiException(
              HttpStatus.SERVICE_UNAVAILABLE,
              "ORDER_PAYMENT_CONFLICT",
              "Order rejected the payment transition; reconciliation is required");
        }
        // Only the canonical 204 confirms that Order accepted this transition.
        if (response.statusCode() != 204) throw unavailable();
      }
    } catch (InterruptedException e) {
      Thread.currentThread().interrupt();
      throw unavailable();
    } catch (IOException e) {
      throw unavailable();
    }
  }

  private ApiException unavailable() {
    return new ApiException(
        HttpStatus.SERVICE_UNAVAILABLE,
        "ORDER_UNAVAILABLE",
        "Payment result was not accepted; retry delivery");
  }

  @Override
  @PreDestroy
  public void close() {
    client.shutdownNow();
  }
}
