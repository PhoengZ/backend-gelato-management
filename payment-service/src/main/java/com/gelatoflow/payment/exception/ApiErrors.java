package com.gelatoflow.payment.exception;

import com.gelatoflow.payment.dto.ErrorResponse;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.http.*;
import org.springframework.http.converter.HttpMessageNotReadableException;
import org.springframework.web.bind.MethodArgumentNotValidException;
import org.springframework.web.bind.annotation.*;

@RestControllerAdvice
public class ApiErrors {
  private static final Logger LOG = LoggerFactory.getLogger(ApiErrors.class);

  @ExceptionHandler(ApiException.class)
  ResponseEntity<ErrorResponse> handle(ApiException e) {
    return ResponseEntity.status(e.status()).body(new ErrorResponse(e.code(), e.getMessage()));
  }

  @ExceptionHandler({MethodArgumentNotValidException.class, HttpMessageNotReadableException.class})
  ResponseEntity<ErrorResponse> invalid(Exception e) {
    return ResponseEntity.badRequest()
        .body(
            new ErrorResponse(
                "INVALID_REQUEST", "Provide a valid order_id UUID and no additional fields"));
  }

  @ExceptionHandler(Exception.class)
  ResponseEntity<ErrorResponse> unexpected(Exception e) {
    // Exception messages/bodies can contain provider keys, tokens, or client secrets.
    LOG.error("Unhandled payment error of type {}", e.getClass().getSimpleName());
    return ResponseEntity.internalServerError()
        .body(new ErrorResponse("INTERNAL_ERROR", "Payment request could not be processed"));
  }
}
