package com.gelatoflow.payment.controller;

import static com.gelatoflow.payment.testsupport.CheckoutFixtures.paidSession;
import static com.gelatoflow.payment.testsupport.Fixtures.*;
import static com.gelatoflow.payment.testsupport.StripeWebhookFixtures.*;
import static org.assertj.core.api.Assertions.*;
import static org.mockito.Mockito.*;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.*;

import com.gelatoflow.payment.client.order.OrderPort;
import com.gelatoflow.payment.client.stripe.CheckoutWebhookPort;
import com.gelatoflow.payment.client.stripe.StripePort;
import com.sun.net.httpserver.HttpServer;
import java.net.InetSocketAddress;
import java.time.Instant;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicReference;
import org.junit.jupiter.api.*;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.autoconfigure.web.servlet.AutoConfigureMockMvc;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.test.context.DynamicPropertyRegistry;
import org.springframework.test.context.DynamicPropertySource;
import org.springframework.test.context.bean.override.mockito.MockitoBean;
import org.springframework.test.web.servlet.MockMvc;

@SpringBootTest(
    properties = {
      "payment.checkout.enabled=false",
      "payment.order-callback.enabled=true",
      "payment.order-callback.allow-plaintext=true",
      "payment.order-callback.service-token=payment-to-order-test-token-at-least-32-bytes",
      "payment.stripe.secret-key=sk_test_fixture",
      "payment.stripe.webhook-secret=whsec_test_fixture",
      "payment.order.service-token=fixture",
      "payment.order.tls=false",
      "payment.jwt.secret=test-signing-secret-at-least-32-bytes"
    })
@AutoConfigureMockMvc
class CheckoutWebhookHttpTest {
  static HttpServer order;
  static AtomicInteger status = new AtomicInteger(204);
  static AtomicInteger calls = new AtomicInteger();
  static AtomicReference<String> path = new AtomicReference<>();
  static AtomicReference<String> credential = new AtomicReference<>();
  @Autowired MockMvc mvc;
  @MockitoBean CheckoutWebhookPort stripe;
  @MockitoBean OrderPort legacyOrders;
  @MockitoBean StripePort legacyStripe;

  @DynamicPropertySource
  static void properties(DynamicPropertyRegistry registry) throws Exception {
    order = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
    order.createContext(
        "/",
        exchange -> {
          calls.incrementAndGet();
          path.set(exchange.getRequestMethod() + " " + exchange.getRequestURI());
          credential.set(exchange.getRequestHeaders().getFirst("X-Payment-Service-Token"));
          exchange.getRequestBody().readAllBytes();
          exchange.sendResponseHeaders(status.get(), -1);
          exchange.close();
        });
    order.start();
    registry.add(
        "payment.order-callback.base-url",
        () -> "http://127.0.0.1:" + order.getAddress().getPort());
  }

  @BeforeEach
  void setup() {
    status.set(204);
    calls.set(0);
    when(stripe.retrieve("cs_test_fixture")).thenReturn(paidSession());
  }

  @AfterAll
  static void cleanup() {
    order.stop(0);
  }

  void deliver(String type, int expected) throws Exception {
    var body = checkoutPayload(type);
    mvc.perform(
            post("/api/v1/payments/webhook")
                .contentType("application/json")
                .header("Stripe-Signature", sign(body, Instant.now().getEpochSecond()))
                .content(body))
        .andExpect(status().is(expected));
  }

  @Test
  void actualWebhookRouteAndHttpClientAcceptOnlyAfterOrder204AndReplayDuplicates()
      throws Exception {
    deliver("checkout.session.completed", 200);
    deliver("checkout.session.completed", 200);
    assertThat(calls.get()).isEqualTo(2);
    assertThat(path.get()).isEqualTo("POST /api/v1/orders/" + ORDER + "/payment-succeeded");
    assertThat(credential.get()).isEqualTo("payment-to-order-test-token-at-least-32-bytes");
    verifyNoInteractions(legacyOrders, legacyStripe);
  }

  @Test
  void orderFailureDoesNotAcknowledgeUntilAReplayIsAccepted() throws Exception {
    status.set(503);
    deliver("checkout.session.completed", 503);
    status.set(204);
    deliver("checkout.session.completed", 200);
    assertThat(calls.get()).isEqualTo(2);
  }

  @Test
  void unpaidExpiredSessionUsesFailureCallbackAndAConflictRequestsReconciliation()
      throws Exception {
    var session = paidSession();
    session.setStatus("expired");
    session.setPaymentStatus("unpaid");
    session.getPaymentIntentObject().setStatus("canceled");
    session.getPaymentIntentObject().setAmountReceived(0L);
    when(stripe.retrieve("cs_test_fixture")).thenReturn(session);
    deliver("checkout.session.expired", 200);
    assertThat(path.get()).endsWith("/payment-failed");
    when(stripe.retrieve("cs_test_fixture")).thenReturn(paidSession());
    status.set(409);
    deliver("checkout.session.completed", 503);
  }
}
