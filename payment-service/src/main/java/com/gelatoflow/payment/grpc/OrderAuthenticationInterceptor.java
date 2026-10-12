package com.gelatoflow.payment.grpc;

import io.grpc.*;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;

public final class OrderAuthenticationInterceptor implements ServerInterceptor {
  private static final Metadata.Key<String> AUTHORIZATION =
      Metadata.Key.of("authorization", Metadata.ASCII_STRING_MARSHALLER);
  private final byte[] expected;

  public OrderAuthenticationInterceptor(String token) {
    expected = ("Bearer " + token).getBytes(StandardCharsets.UTF_8);
  }

  @Override
  public <ReqT, RespT> ServerCall.Listener<ReqT> interceptCall(
      ServerCall<ReqT, RespT> call, Metadata headers, ServerCallHandler<ReqT, RespT> next) {
    var supplied = headers.get(AUTHORIZATION);
    if (supplied == null
        || !MessageDigest.isEqual(expected, supplied.getBytes(StandardCharsets.UTF_8))) {
      call.close(
          Status.UNAUTHENTICATED.withDescription("Order machine authentication required"),
          new Metadata());
      return new ServerCall.Listener<>() {};
    }
    return next.startCall(call, headers);
  }
}
