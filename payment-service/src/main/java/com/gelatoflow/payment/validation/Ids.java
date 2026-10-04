package com.gelatoflow.payment.validation;

import java.util.UUID;

public final class Ids {
  private Ids() {}

  public static boolean isUuid(String value) {
    if (value == null || !value.matches("[0-9a-fA-F]{8}(-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}"))
      return false;
    return !UUID.fromString(value).equals(new UUID(0, 0));
  }
}
