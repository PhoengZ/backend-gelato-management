package com.gelatoflow.payment.service;

import static com.gelatoflow.payment.testsupport.Fixtures.ORDER;
import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatCode;
import static org.assertj.core.api.Assertions.assertThatThrownBy;
import static org.mockito.Mockito.*;

import com.gelatoflow.payment.client.order.HttpOrderCallbackClient;
import com.gelatoflow.payment.client.order.OrderCallbackPort;
import com.gelatoflow.payment.config.OrderCallbackProperties;
import com.gelatoflow.payment.exception.ApiException;
import com.sun.net.httpserver.HttpServer;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.springframework.http.HttpStatus;

class OrderCallbackClientTest {
  private static final String PAYMENT_INTENT_ID = "pi_3MtwBwLkdIwHu7ix28a3tqPa";
  private static final String TOKEN = "payment-to-order-test-token-at-least-32-bytes";

  private OrderCallbackPort mockCallbackPort;
  private RefundService mockRefundService;
  private OrderCallbackClient client;

  private HttpServer server;
  private HttpOrderCallbackClient httpClient;
  private AtomicInteger serverResponseCode;

  @BeforeEach
  void setUp() {
    mockCallbackPort = mock(OrderCallbackPort.class);
    mockRefundService = mock(RefundService.class);
    client = new OrderCallbackClient(mockCallbackPort, mockRefundService);
  }

  @AfterEach
  void tearDown() {
    if (server != null) {
      server.stop(0);
    }
    if (httpClient != null) {
      httpClient.close();
    }
  }

  @Test
  void successfulPaymentNotificationDoesNotTriggerRefund() {
    doNothing().when(mockCallbackPort).succeeded(ORDER);

    assertThatCode(() -> client.notifyPaymentSucceeded(ORDER, PAYMENT_INTENT_ID))
        .doesNotThrowAnyException();

    verify(mockCallbackPort).succeeded(ORDER);
    verifyNoInteractions(mockRefundService);
  }

  @Test
  void conflictCatchesExceptionAndImmediatelyTriggersRefund() {
    doThrow(
            new ApiException(
                HttpStatus.SERVICE_UNAVAILABLE,
                "ORDER_PAYMENT_CONFLICT",
                "Order rejected the payment transition; reconciliation is required"))
        .when(mockCallbackPort)
        .succeeded(ORDER);

    assertThatCode(() -> client.notifyPaymentSucceeded(ORDER, PAYMENT_INTENT_ID))
        .doesNotThrowAnyException();

    verify(mockCallbackPort).succeeded(ORDER);
    verify(mockRefundService).refund(PAYMENT_INTENT_ID);
  }

  @Test
  void httpConflictStatusAlsoTriggersRefund() {
    doThrow(new ApiException(HttpStatus.CONFLICT, "CONFLICT", "Reservation expired"))
        .when(mockCallbackPort)
        .succeeded(ORDER);

    assertThatCode(() -> client.notifyPaymentSucceeded(ORDER, PAYMENT_INTENT_ID))
        .doesNotThrowAnyException();

    verify(mockCallbackPort).succeeded(ORDER);
    verify(mockRefundService).refund(PAYMENT_INTENT_ID);
  }

  @Test
  void otherErrorsPropagateWithoutTriggeringRefund() {
    doThrow(
            new ApiException(
                HttpStatus.SERVICE_UNAVAILABLE,
                "ORDER_UNAVAILABLE",
                "Payment result was not accepted; retry delivery"))
        .when(mockCallbackPort)
        .succeeded(ORDER);

    assertThatThrownBy(() -> client.notifyPaymentSucceeded(ORDER, PAYMENT_INTENT_ID))
        .isInstanceOf(ApiException.class)
        .satisfies(
            e -> {
              ApiException ex = (ApiException) e;
              assertThat(ex.code()).isEqualTo("ORDER_UNAVAILABLE");
            });

    verify(mockCallbackPort).succeeded(ORDER);
    verifyNoInteractions(mockRefundService);
  }

  @Test
  void notifiesPaymentFailedWithoutTriggeringRefund() {
    doNothing().when(mockCallbackPort).failed(ORDER);

    client.notifyPaymentFailed(ORDER);

    verify(mockCallbackPort).failed(ORDER);
    verifyNoInteractions(mockRefundService);
  }

  @Test
  void endToEndHttp409ResponseCatchesConflictAndTriggersRefund() throws Exception {
    serverResponseCode = new AtomicInteger(409);
    server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
    server.createContext(
        "/",
        exchange -> {
          byte[] body = "{\"error\":\"reservation_expired\"}".getBytes(StandardCharsets.UTF_8);
          exchange.sendResponseHeaders(serverResponseCode.get(), body.length);
          try (var os = exchange.getResponseBody()) {
            os.write(body);
          }
        });
    server.start();

    OrderCallbackProperties properties =
        new OrderCallbackProperties(
            true, "http://127.0.0.1:" + server.getAddress().getPort(), TOKEN, true, 1000);
    httpClient = new HttpOrderCallbackClient(properties);

    OrderCallbackClient liveClient = new OrderCallbackClient(httpClient, mockRefundService);

    // Call notifyPaymentSucceeded against real HTTP server returning 409
    assertThatCode(() -> liveClient.notifyPaymentSucceeded(ORDER, PAYMENT_INTENT_ID))
        .doesNotThrowAnyException();

    verify(mockRefundService).refund(PAYMENT_INTENT_ID);
  }
}
