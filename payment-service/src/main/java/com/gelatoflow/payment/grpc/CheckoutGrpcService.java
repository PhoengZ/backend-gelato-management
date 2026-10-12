package com.gelatoflow.payment.grpc;

import com.gelatoflow.contracts.payment.v1.*;
import com.gelatoflow.payment.service.CheckoutService;
import io.grpc.Context;
import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import io.grpc.stub.StreamObserver;
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.stereotype.Component;

@Component
@ConditionalOnProperty(
    name = "payment.checkout.enabled",
    havingValue = "true",
    matchIfMissing = true)
public class CheckoutGrpcService extends PaymentServiceGrpc.PaymentServiceImplBase {
  private final CheckoutService checkout;

  public CheckoutGrpcService(CheckoutService checkout) {
    this.checkout = checkout;
  }

  @Override
  public void createCheckoutSession(
      CreateCheckoutSessionRequest request,
      StreamObserver<CreateCheckoutSessionResponse> observer) {
    try {
      if (Context.current().isCancelled()) return;
      var response = checkout.create(request);
      if (Context.current().isCancelled()) return;
      observer.onNext(response);
      observer.onCompleted();
    } catch (StatusRuntimeException e) {
      observer.onError(e);
    } catch (RuntimeException e) {
      // Provider exception messages can contain credentials or payment URLs.
      observer.onError(
          Status.INTERNAL.withDescription("Checkout could not be completed").asRuntimeException());
    }
  }
}
