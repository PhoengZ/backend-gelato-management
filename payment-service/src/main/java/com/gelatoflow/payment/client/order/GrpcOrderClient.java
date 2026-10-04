package com.gelatoflow.payment.client.order;

import com.gelatoflow.contracts.order.v1.*;
import com.gelatoflow.payment.config.PaymentProperties;
import com.gelatoflow.payment.exception.ApiException;
import io.grpc.*;
import io.grpc.stub.MetadataUtils;
import java.util.concurrent.TimeUnit;
import org.springframework.http.HttpStatus;
import org.springframework.stereotype.Component;

@Component
public class GrpcOrderClient implements OrderPort {
  private final OrderServiceGrpc.OrderServiceBlockingStub stub;
  private final long deadlineMs;

  public GrpcOrderClient(ManagedChannel channel, PaymentProperties properties) {
    var metadata = new Metadata();
    metadata.put(
        Metadata.Key.of("authorization", Metadata.ASCII_STRING_MARSHALLER),
        "Bearer " + properties.order().serviceToken());
    stub =
        OrderServiceGrpc.newBlockingStub(channel)
            .withInterceptors(MetadataUtils.newAttachHeadersInterceptor(metadata));
    deadlineMs = properties.order().deadlineMs();
  }

  private OrderServiceGrpc.OrderServiceBlockingStub call() {
    return stub.withDeadlineAfter(deadlineMs, TimeUnit.MILLISECONDS);
  }

  @Override
  public GetOrderDetailsResponse get(String orderId) {
    try {
      return call()
          .getOrderDetails(GetOrderDetailsRequest.newBuilder().setOrderId(orderId).build());
    } catch (StatusRuntimeException e) {
      throw translate(e);
    }
  }

  @Override
  public void bind(BindPaymentIntentRequest request) {
    try {
      var response = call().bindPaymentIntent(request);
      if (!request.getPaymentIntentId().equals(response.getPaymentIntentId())) {
        throw new ApiException(
            HttpStatus.BAD_GATEWAY,
            "ORDER_INVALID_RESPONSE",
            "Order returned an unexpected payment binding");
      }
    } catch (StatusRuntimeException e) {
      throw translate(e);
    }
  }

  @Override
  public void report(ReportPaymentResultRequest request) {
    try {
      call().reportPaymentResult(request);
    } catch (StatusRuntimeException e) {
      // Never acknowledge a webhook that Order has not durably accepted.
      throw new ApiException(
          HttpStatus.SERVICE_UNAVAILABLE,
          "ORDER_UNAVAILABLE",
          "Payment result was not accepted; retry delivery");
    }
  }

  private ApiException translate(StatusRuntimeException e) {
    return switch (e.getStatus().getCode()) {
      case NOT_FOUND ->
          new ApiException(HttpStatus.NOT_FOUND, "ORDER_NOT_FOUND", "Order does not exist");
      case FAILED_PRECONDITION, ALREADY_EXISTS, ABORTED ->
          new ApiException(
              HttpStatus.CONFLICT, "ORDER_NOT_PAYABLE", "Order cannot accept this payment intent");
      default ->
          new ApiException(
              HttpStatus.SERVICE_UNAVAILABLE,
              "ORDER_UNAVAILABLE",
              "Order service is temporarily unavailable");
    };
  }
}
