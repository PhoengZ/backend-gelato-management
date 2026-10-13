package com.gelatoflow.payment.service;

import static com.gelatoflow.payment.testsupport.Fixtures.properties;
import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.argThat;
import static org.mockito.Mockito.mockStatic;

import com.gelatoflow.payment.exception.ApiException;
import com.stripe.exception.ApiConnectionException;
import com.stripe.exception.IdempotencyException;
import com.stripe.model.Refund;
import com.stripe.net.RequestOptions;
import com.stripe.param.RefundCreateParams;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.mockito.MockedStatic;
import org.springframework.http.HttpStatus;

class RefundServiceTest {
  private RefundService refundService;

  @BeforeEach
  void setUp() {
    refundService = new RefundService(properties());
  }

  @Test
  void createsRefundWithPaymentIntentIdAndDeterministicIdempotencyKey() {
    String paymentIntentId = "pi_3MtwBwLkdIwHu7ix28a3tqPa";
    Refund expectedRefund = new Refund();
    expectedRefund.setId("re_12345");
    expectedRefund.setStatus("succeeded");

    try (MockedStatic<Refund> mockedRefund = mockStatic(Refund.class)) {
      mockedRefund
          .when(() -> Refund.create(any(RefundCreateParams.class), any(RequestOptions.class)))
          .thenReturn(expectedRefund);

      Refund actual = refundService.refund(paymentIntentId);

      assertThat(actual).isSameAs(expectedRefund);
      mockedRefund.verify(
          () ->
              Refund.create(
                  argThat(
                      (RefundCreateParams params) ->
                          paymentIntentId.equals(params.getPaymentIntent())),
                  argThat(
                      (RequestOptions options) ->
                          ("refund-" + paymentIntentId).equals(options.getIdempotencyKey()))));
    }
  }

  @Test
  void throwsBadRequestWhenPaymentIntentIdIsNullOrBlank() {
    assertThatThrownBy(() -> refundService.refund(null))
        .isInstanceOf(ApiException.class)
        .satisfies(
            e -> {
              ApiException ex = (ApiException) e;
              assertThat(ex.status()).isEqualTo(HttpStatus.BAD_REQUEST);
              assertThat(ex.code()).isEqualTo("INVALID_PAYMENT_INTENT");
            });

    assertThatThrownBy(() -> refundService.refund("   "))
        .isInstanceOf(ApiException.class)
        .satisfies(
            e -> {
              ApiException ex = (ApiException) e;
              assertThat(ex.status()).isEqualTo(HttpStatus.BAD_REQUEST);
              assertThat(ex.code()).isEqualTo("INVALID_PAYMENT_INTENT");
            });
  }

  @Test
  void handlesIdempotencyConflictProperly() {
    String paymentIntentId = "pi_conflict_test";

    try (MockedStatic<Refund> mockedRefund = mockStatic(Refund.class)) {
      mockedRefund
          .when(() -> Refund.create(any(RefundCreateParams.class), any(RequestOptions.class)))
          .thenThrow(new IdempotencyException("Keys conflict", "req_test", "code_conflict", 409));

      assertThatThrownBy(() -> refundService.refund(paymentIntentId))
          .isInstanceOf(ApiException.class)
          .satisfies(
              e -> {
                ApiException ex = (ApiException) e;
                assertThat(ex.status()).isEqualTo(HttpStatus.CONFLICT);
                assertThat(ex.code()).isEqualTo("REFUND_CONFLICT");
              });
    }
  }

  @Test
  void handlesGeneralStripeProviderError() {
    String paymentIntentId = "pi_provider_error";

    try (MockedStatic<Refund> mockedRefund = mockStatic(Refund.class)) {
      mockedRefund
          .when(() -> Refund.create(any(RefundCreateParams.class), any(RequestOptions.class)))
          .thenThrow(new ApiConnectionException("Stripe API down"));

      assertThatThrownBy(() -> refundService.refund(paymentIntentId))
          .isInstanceOf(ApiException.class)
          .satisfies(
              e -> {
                ApiException ex = (ApiException) e;
                assertThat(ex.status()).isEqualTo(HttpStatus.BAD_GATEWAY);
                assertThat(ex.code()).isEqualTo("PAYMENT_PROVIDER_ERROR");
              });
    }
  }
}
