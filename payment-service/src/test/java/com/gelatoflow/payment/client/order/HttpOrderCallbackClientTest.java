package com.gelatoflow.payment.client.order;

import static com.gelatoflow.payment.testsupport.Fixtures.*;
import static org.assertj.core.api.Assertions.*;

import com.gelatoflow.payment.config.OrderCallbackProperties;
import com.gelatoflow.payment.exception.ApiException;
import com.sun.net.httpserver.HttpServer;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicReference;
import org.junit.jupiter.api.*;
import org.springframework.http.HttpStatus;

class HttpOrderCallbackClientTest {
  static final String TOKEN = "payment-to-order-test-token-at-least-32-bytes";
  HttpServer server;
  HttpOrderCallbackClient client;
  AtomicInteger response = new AtomicInteger(204);
  AtomicInteger calls = new AtomicInteger();
  AtomicReference<String> request = new AtomicReference<>();
  AtomicInteger delay = new AtomicInteger();
  AtomicBoolean holdBody = new AtomicBoolean();
  CountDownLatch releaseBody = new CountDownLatch(1);

  @BeforeEach
  void setup() throws Exception {
    server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
    server.createContext(
        "/",
        exchange -> {
          calls.incrementAndGet();
          request.set(
              exchange.getRequestMethod()
                  + " "
                  + exchange.getRequestURI()
                  + " "
                  + exchange.getRequestHeaders().getFirst("X-Payment-Service-Token")
                  + " "
                  + exchange.getRequestHeaders().getFirst("Authorization")
                  + " "
                  + new String(exchange.getRequestBody().readAllBytes(), StandardCharsets.UTF_8));
          try {
            Thread.sleep(delay.get());
          } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
          }
          exchange.getResponseHeaders().set("Location", "/do-not-follow");
          var body = "private-upstream-response".getBytes(StandardCharsets.UTF_8);
          try {
            exchange.sendResponseHeaders(
                response.get(), response.get() == 204 ? -1 : holdBody.get() ? 0 : body.length);
            if (response.get() != 204) {
              exchange.getResponseBody().write(body);
              exchange.getResponseBody().flush();
              if (holdBody.get()) {
                try {
                  releaseBody.await();
                } catch (InterruptedException e) {
                  Thread.currentThread().interrupt();
                }
              }
            }
          } catch (java.io.IOException ignored) {
            /* The timeout test deliberately disconnects. */
          } finally {
            exchange.close();
          }
        });
    server.start();
    client = createClient(1000);
  }

  HttpOrderCallbackClient createClient(long timeout) {
    return new HttpOrderCallbackClient(
        new OrderCallbackProperties(
            true, "http://127.0.0.1:" + server.getAddress().getPort(), TOKEN, true, timeout));
  }

  @AfterEach
  void cleanup() {
    releaseBody.countDown();
    client.close();
    server.stop(0);
  }

  @Test
  void postsCanonicalPathsEmptyBodyAndOnlyTheDedicatedServiceCredential() {
    client.succeeded(ORDER.toUpperCase());
    assertThat(request.get())
        .isEqualTo("POST /api/v1/orders/" + ORDER + "/payment-succeeded " + TOKEN + " null ");
    client.failed(ORDER);
    assertThat(request.get()).contains("/payment-failed");
    assertThat(calls.get()).isEqualTo(2);
  }

  @Test
  void only204AcknowledgesAndRedirectsOrErrorsNeverLeakSecretsOrFollowRedirects() {
    for (var status : new int[] {200, 201, 302, 401, 403, 404, 409, 500, 503, 504}) {
      response.set(status);
      assertThatThrownBy(() -> client.succeeded(ORDER))
          .isInstanceOfSatisfying(
              ApiException.class,
              e -> {
                assertThat(e.status()).isEqualTo(HttpStatus.SERVICE_UNAVAILABLE);
                assertThat(e.getMessage()).doesNotContain("private", TOKEN);
                assertThat(e.code())
                    .isEqualTo(status == 409 ? "ORDER_PAYMENT_CONFLICT" : "ORDER_UNAVAILABLE");
              });
    }
    assertThat(calls.get()).isEqualTo(10);
    assertThat(request.get()).doesNotContain("do-not-follow");
  }

  @Test
  void timeoutConnectionFailureAndInvalidIdsDoNotAcknowledgeTheWebhook() {
    assertThatThrownBy(() -> client.succeeded("../other")).isInstanceOf(ApiException.class);
    assertThat(calls.get()).isZero();
    client.close();
    client = createClient(100);
    delay.set(350);
    assertThatThrownBy(() -> client.succeeded(ORDER)).isInstanceOf(ApiException.class);
    server.stop(0);
    assertThatThrownBy(() -> client.succeeded(ORDER)).isInstanceOf(ApiException.class);
  }

  @Test
  void handlesConflictWithoutWaitingForTheErrorBodyToFinish() {
    response.set(409);
    holdBody.set(true);
    try {
      org.junit.jupiter.api.Assertions.assertTimeoutPreemptively(
          java.time.Duration.ofSeconds(2),
          () ->
              assertThatThrownBy(() -> client.succeeded(ORDER))
                  .isInstanceOfSatisfying(
                      ApiException.class,
                      e -> assertThat(e.code()).isEqualTo("ORDER_PAYMENT_CONFLICT")));
    } finally {
      releaseBody.countDown();
    }
  }
}
