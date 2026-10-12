package com.gelatoflow.payment.dto;

import com.fasterxml.jackson.annotation.JsonProperty;
import jakarta.validation.constraints.NotBlank;
import jakarta.validation.constraints.Size;

public record CreatePaymentIntentRequest(
    @JsonProperty("order_id") @NotBlank @Size(max = 36) String orderId) {}
