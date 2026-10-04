package com.gelatoflow.payment.service;

import static com.gelatoflow.payment.testsupport.Fixtures.*;
import static org.assertj.core.api.Assertions.*;
import static org.mockito.Mockito.*;

import com.gelatoflow.contracts.order.v1.BindPaymentIntentRequest;
import com.gelatoflow.payment.client.order.OrderPort;
import com.gelatoflow.payment.client.stripe.StripePort;
import com.gelatoflow.payment.exception.ApiException;
import org.junit.jupiter.api.*;
import org.springframework.http.HttpStatus;

class PaymentServiceTest {
  OrderPort orders = mock(OrderPort.class);
  StripePort stripe = mock(StripePort.class);
  PaymentService service = new PaymentService(orders, stripe, properties());

  @BeforeEach
  void setup() {
    when(orders.get(ORDER)).thenReturn(order());
    when(stripe.create(ORDER, CUSTOMER, 25000, "THB"))
        .thenReturn(intent("requires_payment_method"));
  }

  @Test
  void authoritativeTotalAndBindingPrecedeSecretResponse() {
    var response = service.create(ORDER, CUSTOMER);
    assertThat(response.amountMinor()).isEqualTo(25000);
    assertThat(response.currency()).isEqualTo("THB");
    var sequence = inOrder(orders, stripe);
    sequence.verify(orders).get(ORDER);
    sequence.verify(stripe).create(ORDER, CUSTOMER, 25000, "THB");
    sequence
        .verify(orders)
        .bind(
            BindPaymentIntentRequest.newBuilder()
                .setOrderId(ORDER)
                .setPaymentIntentId("pi_fixture")
                .setAmountMinor(25000)
                .setCurrency("THB")
                .build());
  }

  @Test
  void anotherCustomerCannotCreateOrReadIntent() {
    assertThatThrownBy(() -> service.create(ORDER, OTHER))
        .isInstanceOfSatisfying(
            ApiException.class, e -> assertThat(e.status()).isEqualTo(HttpStatus.NOT_FOUND));
    verifyNoInteractions(stripe);
  }

  @Test
  void invalidAndNilUuidNeverReachOrder() {
    for (String id : new String[] {"1-1-1-1-1", "00000000-0000-0000-0000-000000000000", "bad"}) {
      assertThatThrownBy(() -> service.create(id, CUSTOMER)).isInstanceOf(ApiException.class);
    }
    verifyNoInteractions(orders, stripe);
  }

  @Test
  void rejectsPaidOrderAndInvalidTotalsBeforeProviderCall() {
    when(orders.get(ORDER)).thenReturn(order().toBuilder().setStatus("PAID").build());
    assertThatThrownBy(() -> service.create(ORDER, CUSTOMER)).isInstanceOf(ApiException.class);
    when(orders.get(ORDER)).thenReturn(order().toBuilder().setTotalAmountMinor(0).build());
    assertThatThrownBy(() -> service.create(ORDER, CUSTOMER)).isInstanceOf(ApiException.class);
    when(orders.get(ORDER)).thenReturn(order().toBuilder().setCurrency("ZZZ").build());
    assertThatThrownBy(() -> service.create(ORDER, CUSTOMER)).isInstanceOf(ApiException.class);
    verifyNoInteractions(stripe);
  }

  @Test
  void existingBindingIsRetrievedWithoutCreatingNewPayment() {
    when(orders.get(ORDER))
        .thenReturn(order().toBuilder().setPaymentIntentId("pi_fixture").build());
    when(stripe.retrieve("pi_fixture")).thenReturn(intent("requires_action"));
    assertThat(service.create(ORDER, CUSTOMER).paymentIntentId()).isEqualTo("pi_fixture");
    verify(stripe, never()).create(anyString(), anyString(), anyLong(), anyString());
  }

  @Test
  void bindingFailureNeverReturnsSecret() {
    doThrow(new ApiException(HttpStatus.CONFLICT, "ORDER_NOT_PAYABLE", "Conflict"))
        .when(orders)
        .bind(any());
    assertThatThrownBy(() -> service.create(ORDER, CUSTOMER)).isInstanceOf(ApiException.class);
  }

  @Test
  void mismatchedProviderAmountAndModeCannotBeBound() {
    var wrong = intent("requires_payment_method");
    wrong.setAmount(1L);
    when(stripe.create(anyString(), anyString(), anyLong(), anyString())).thenReturn(wrong);
    assertThatThrownBy(() -> service.create(ORDER, CUSTOMER)).isInstanceOf(ApiException.class);
    wrong.setAmount(25000L);
    wrong.setLivemode(true);
    assertThatThrownBy(() -> service.create(ORDER, CUSTOMER)).isInstanceOf(ApiException.class);
    verify(orders, never()).bind(any());
  }

  @Test
  void processingOrCompletedIntentDoesNotReturnSecret() {
    when(stripe.create(anyString(), anyString(), anyLong(), anyString()))
        .thenReturn(intent("succeeded"));
    assertThatThrownBy(() -> service.create(ORDER, CUSTOMER)).isInstanceOf(ApiException.class);
    verify(orders, never()).bind(any());
  }
}
