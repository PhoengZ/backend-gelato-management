package com.gelatoflow.payment.service;

import static com.gelatoflow.payment.testsupport.Fixtures.*;
import static com.gelatoflow.payment.testsupport.StripeWebhookFixtures.*;
import static org.assertj.core.api.Assertions.*;
import static org.mockito.Mockito.*;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.gelatoflow.contracts.order.v1.*;
import com.gelatoflow.payment.client.order.OrderPort;
import com.gelatoflow.payment.client.stripe.StripePort;
import com.gelatoflow.payment.exception.ApiException;
import java.time.Instant;
import org.junit.jupiter.api.Test;
import org.mockito.ArgumentCaptor;
import org.springframework.http.HttpStatus;

class WebhookServiceTest {
  OrderPort orders = mock(OrderPort.class);
  StripePort stripe = mock(StripePort.class);
  WebhookService service =
      new WebhookService(
          properties(),
          new ObjectMapper(),
          stripe,
          orders,
          new PaymentService(orders, stripe, properties()));

  @Test
  void signedSuccessReportsVerifiedTotalAndStableEventIdOnEveryDelivery() throws Exception {
    when(stripe.retrieve("pi_fixture")).thenReturn(intent("succeeded"));
    var payload = payload("payment_intent.succeeded");
    var signature = sign(payload, Instant.now().getEpochSecond());
    service.receive(payload, signature);
    service.receive(payload, signature);
    var report = ArgumentCaptor.forClass(ReportPaymentResultRequest.class);
    verify(orders, times(2)).report(report.capture());
    assertThat(report.getValue().getOutcome()).isEqualTo(PaymentOutcome.PAYMENT_OUTCOME_SUCCEEDED);
    assertThat(report.getValue().getOrderId()).isEqualTo(ORDER);
    assertThat(report.getValue().getAmountMinor()).isEqualTo(25000);
    assertThat(report.getValue().getCurrency()).isEqualTo("THB");
    assertThat(report.getValue().getProviderEventId()).isEqualTo("evt_fixture");
    assertThat(report.getValue().getOccurredAt()).isEqualTo("2023-11-14T22:13:20Z");
  }

  @Test
  void rejectsMissingInvalidExpiredOrBodyTamperedSignaturesWithoutSideEffects() throws Exception {
    var body = payload("payment_intent.succeeded");
    var signature = sign(body, Instant.now().getEpochSecond());
    for (String candidate :
        new String[] {null, "bad", sign(body, Instant.now().minusSeconds(600).getEpochSecond())}) {
      assertThatThrownBy(() -> service.receive(body, candidate)).isInstanceOf(ApiException.class);
    }
    assertThatThrownBy(() -> service.receive(body + " ", signature))
        .isInstanceOf(ApiException.class);
    verifyNoInteractions(stripe, orders);
  }

  @Test
  void failureIsReportedButStaleFailureAfterSuccessIsIgnored() throws Exception {
    var body = payload("payment_intent.payment_failed");
    when(stripe.retrieve("pi_fixture")).thenReturn(intent("requires_payment_method"));
    service.receive(body, sign(body, Instant.now().getEpochSecond()));
    verify(orders).report(argThat(r -> r.getOutcome() == PaymentOutcome.PAYMENT_OUTCOME_FAILED));
    reset(orders);
    when(stripe.retrieve("pi_fixture")).thenReturn(intent("succeeded"));
    service.receive(body, sign(body, Instant.now().getEpochSecond()));
    verifyNoInteractions(orders);
  }

  @Test
  void orderFailurePropagatesSoStripeRetries() throws Exception {
    when(stripe.retrieve("pi_fixture")).thenReturn(intent("succeeded"));
    doThrow(new ApiException(HttpStatus.SERVICE_UNAVAILABLE, "ORDER_UNAVAILABLE", "retry"))
        .when(orders)
        .report(any());
    var body = payload("payment_intent.succeeded");
    var signature = sign(body, Instant.now().getEpochSecond());
    assertThatThrownBy(() -> service.receive(body, signature))
        .isInstanceOfSatisfying(
            ApiException.class,
            e -> assertThat(e.status()).isEqualTo(HttpStatus.SERVICE_UNAVAILABLE));
  }

  @Test
  void partialCaptureCannotBeReportedAsFullyPaid() throws Exception {
    var intent = intent("succeeded");
    intent.setAmountReceived(100L);
    when(stripe.retrieve("pi_fixture")).thenReturn(intent);
    var body = payload("payment_intent.succeeded");
    var signature = sign(body, Instant.now().getEpochSecond());
    assertThatThrownBy(() -> service.receive(body, signature)).isInstanceOf(ApiException.class);
    verifyNoInteractions(orders);
  }

  @Test
  void unsupportedSignedEventsAreAcknowledgedWithoutSideEffects() throws Exception {
    var body = payload("customer.created");
    service.receive(body, sign(body, Instant.now().getEpochSecond()));
    verifyNoInteractions(stripe, orders);
  }
}
