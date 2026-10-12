package com.gelatoflow.payment.service;

import static com.gelatoflow.payment.testsupport.CheckoutFixtures.*;
import static com.gelatoflow.payment.testsupport.Fixtures.*;
import static com.gelatoflow.payment.testsupport.StripeWebhookFixtures.*;
import static org.assertj.core.api.Assertions.*;
import static org.mockito.Mockito.*;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.gelatoflow.payment.client.order.OrderCallbackPort;
import com.gelatoflow.payment.client.order.OrderPort;
import com.gelatoflow.payment.client.stripe.CheckoutWebhookPort;
import com.gelatoflow.payment.client.stripe.StripePort;
import com.gelatoflow.payment.config.OrderCallbackProperties;
import com.gelatoflow.payment.exception.ApiException;
import com.stripe.model.checkout.Session;
import java.time.Instant;
import java.util.HashMap;
import java.util.List;
import java.util.function.Consumer;
import org.junit.jupiter.api.Test;
import org.springframework.http.HttpStatus;

class CheckoutWebhookServiceTest {
  CheckoutWebhookPort stripe = mock(CheckoutWebhookPort.class);
  OrderCallbackPort callbacks = mock(OrderCallbackPort.class);
  OrderPort legacyOrders = mock(OrderPort.class);
  StripePort legacyStripe = mock(StripePort.class);
  CheckoutWebhookService checkout =
      new CheckoutWebhookService(
          stripe,
          callbacks,
          new OrderCallbackProperties(true, "https://order.example", TOKEN, false, 1000),
          properties());
  WebhookService webhooks =
      new WebhookService(
          properties(),
          new ObjectMapper(),
          legacyStripe,
          legacyOrders,
          new PaymentService(legacyOrders, legacyStripe, properties()),
          checkout);

  void receive(String type) throws Exception {
    var body = checkoutPayload(type);
    webhooks.receive(body, sign(body, Instant.now().getEpochSecond()));
  }

  @Test
  void signedCheckoutSuccessAndDuplicatesUseHttpCallbackWithoutLegacyOrderGrpc() throws Exception {
    doReturn(paidSession()).when(stripe).retrieve("cs_test_fixture");
    receive("checkout.session.completed");
    receive("checkout.session.completed");
    receive("checkout.session.async_payment_succeeded");
    verify(callbacks, times(3)).succeeded(ORDER);
    verifyNoInteractions(legacyOrders, legacyStripe);
  }

  @Test
  void rawBodySignatureAndEventModeAreVerifiedBeforeRetrievingOrCallingOrder() throws Exception {
    var body = checkoutPayload("checkout.session.completed");
    var signature = sign(body, Instant.now().getEpochSecond());
    for (var bad :
        new String[] {
          null, "invalid", sign(body, Instant.now().minusSeconds(600).getEpochSecond())
        }) {
      assertThatThrownBy(() -> webhooks.receive(body, bad)).isInstanceOf(ApiException.class);
    }
    assertThatThrownBy(() -> webhooks.receive(body + " ", signature))
        .isInstanceOf(ApiException.class);
    var live = body.replace("\"livemode\":false", "\"livemode\":true");
    var liveSignature = sign(live, Instant.now().getEpochSecond());
    assertThatThrownBy(() -> webhooks.receive(live, liveSignature))
        .isInstanceOf(ApiException.class);
    verifyNoInteractions(stripe, callbacks, legacyOrders, legacyStripe);
  }

  @Test
  void inconsistentProviderIdentityMetadataAmountsOrStateCannotMarkOrderPaid() throws Exception {
    List<Consumer<Session>> changes =
        List.of(
            s -> s.setId("cs_other"),
            s -> s.setLivemode(true),
            s -> s.setMode("subscription"),
            s -> s.setAmountTotal(1L),
            s -> {
              s.setAmountTotal(100L);
              s.getPaymentIntentObject().setAmount(100L);
              s.getPaymentIntentObject().setAmountReceived(100L);
            },
            s -> {
              s.setCurrency("usd");
              s.getPaymentIntentObject().setCurrency("usd");
            },
            s -> s.setCurrency("xyz"),
            s -> s.setClientReferenceId(OTHER),
            s -> s.setMetadata(null),
            s -> s.setStatus("open"),
            s -> s.setPaymentIntent("pi_unexpanded"),
            s -> s.getPaymentIntentObject().setAmountReceived(1L),
            s -> s.getPaymentIntentObject().setAmount(1L),
            s -> s.getPaymentIntentObject().setCurrency("usd"),
            s -> s.getPaymentIntentObject().setLivemode(true),
            s -> s.getPaymentIntentObject().setStatus("processing"),
            s -> s.getPaymentIntentObject().getMetadata().put("order_id", OTHER),
            s -> s.getPaymentIntentObject().getMetadata().put("customer_id", OTHER),
            s -> s.getPaymentIntentObject().getMetadata().put("checkout_key", OTHER),
            s -> s.getPaymentIntentObject().getMetadata().put("amount_minor", "1"),
            s -> s.getPaymentIntentObject().getMetadata().put("currency", "USD"),
            s -> s.getPaymentIntentObject().getMetadata().put("reservation_expires_at", "invalid"),
            s -> {
              var m = new HashMap<>(s.getMetadata());
              m.put("checkout_key", "invalid");
              s.setMetadata(m);
            });
    for (var change : changes) {
      var session = paidSession();
      change.accept(session);
      when(stripe.retrieve("cs_test_fixture")).thenReturn(session);
      assertThatThrownBy(() -> receive("checkout.session.completed"))
          .isInstanceOfSatisfying(
              ApiException.class,
              e -> assertThat(e.status()).isEqualTo(HttpStatus.SERVICE_UNAVAILABLE));
    }
    verifyNoInteractions(callbacks, legacyOrders, legacyStripe);
  }

  @Test
  void callbackOrProviderFailurePropagatesForStripeRedelivery() throws Exception {
    when(stripe.retrieve("cs_test_fixture"))
        .thenThrow(new ApiException(HttpStatus.BAD_GATEWAY, "PAYMENT_PROVIDER_ERROR", "retry"));
    assertThatThrownBy(() -> receive("checkout.session.completed"))
        .isInstanceOf(ApiException.class);
    verifyNoInteractions(callbacks);
    doReturn(paidSession()).when(stripe).retrieve("cs_test_fixture");
    doThrow(new ApiException(HttpStatus.SERVICE_UNAVAILABLE, "ORDER_UNAVAILABLE", "retry"))
        .when(callbacks)
        .succeeded(ORDER);
    assertThatThrownBy(() -> receive("checkout.session.completed"))
        .isInstanceOf(ApiException.class);
    reset(callbacks);
    receive("checkout.session.completed");
    verify(callbacks).succeeded(ORDER);
  }

  @Test
  void expiryAndTerminalAsyncFailureReleaseButStaleFailureAfterSuccessDoesNot() throws Exception {
    var expired = session(request());
    expired.setStatus("expired");
    when(stripe.retrieve("cs_test_fixture")).thenReturn(expired);
    receive("checkout.session.expired");
    var failed = paidSession();
    failed.setPaymentStatus("unpaid");
    failed.getPaymentIntentObject().setStatus("requires_payment_method");
    failed.getPaymentIntentObject().setAmountReceived(0L);
    when(stripe.retrieve("cs_test_fixture")).thenReturn(failed);
    receive("checkout.session.async_payment_failed");
    verify(callbacks, times(2)).failed(ORDER);
    reset(callbacks);
    when(stripe.retrieve("cs_test_fixture")).thenReturn(paidSession());
    receive("checkout.session.expired");
    receive("checkout.session.async_payment_failed");
    verifyNoInteractions(callbacks);
  }

  @Test
  void completedButProcessingWaitsForPaymentAndExpiryCannotReleaseProcessingIntent()
      throws Exception {
    var pending = paidSession();
    pending.setPaymentStatus("unpaid");
    pending.getPaymentIntentObject().setStatus("processing");
    pending.getPaymentIntentObject().setAmountReceived(0L);
    when(stripe.retrieve("cs_test_fixture")).thenReturn(pending);
    receive("checkout.session.completed");
    pending.setStatus("expired");
    assertThatThrownBy(() -> receive("checkout.session.expired")).isInstanceOf(ApiException.class);
    verifyNoInteractions(callbacks);
  }

  @Test
  void checkoutPaymentIntentSuccessAndDeclinedAttemptsDoNotUseLegacyReporting() throws Exception {
    for (var type : new String[] {"payment_intent.succeeded", "payment_intent.payment_failed"}) {
      var payment = paidSession().getPaymentIntentObject();
      if (type.endsWith("payment_failed")) {
        payment.setStatus("requires_payment_method");
        payment.setAmountReceived(0L);
      }
      when(legacyStripe.retrieve("pi_fixture")).thenReturn(payment);
      var body = payload(type);
      webhooks.receive(body, sign(body, Instant.now().getEpochSecond()));
    }
    verifyNoInteractions(callbacks, legacyOrders, stripe);
  }

  @Test
  void latePaidSessionIsForwardedToOrderForItsInventoryDecision() throws Exception {
    var paid = paidSession();
    var metadata = new HashMap<>(paid.getMetadata());
    metadata.put("reservation_expires_at", "2020-01-01T00:00:00Z");
    paid.setMetadata(metadata);
    paid.getPaymentIntentObject().setMetadata(metadata);
    when(stripe.retrieve("cs_test_fixture")).thenReturn(paid);
    receive("checkout.session.completed");
    verify(callbacks).succeeded(ORDER);
  }
}
