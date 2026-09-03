#!/usr/bin/env python3

import json
from copy import deepcopy
from pathlib import Path

from jsonschema import Draft202012Validator, FormatChecker


CONTRACTS_DIR = Path(__file__).resolve().parents[1]

EVENT_CONTRACTS = (
    (
        "events/inventory-waste.v1.schema.json",
        "examples/inventory-waste.v1.json",
        ("cost_lost_minor",),
    ),
    (
        "events/order-placed.v1.schema.json",
        "examples/order-placed.v1.json",
        ("total_amount_minor",),
    ),
)


def load_json(path: Path) -> object:
    with path.open(encoding="utf-8") as source:
        return json.load(source)


def main() -> None:
    for schema_name, example_name, money_fields in EVENT_CONTRACTS:
        schema_path = CONTRACTS_DIR / schema_name
        example_path = CONTRACTS_DIR / example_name

        schema = load_json(schema_path)
        example = load_json(example_path)

        Draft202012Validator.check_schema(schema)
        validator = Draft202012Validator(schema, format_checker=FormatChecker())
        validator.validate(example)

        for money_field in money_fields:
            decimal_example = deepcopy(example)
            decimal_example["data"][money_field] = 12.5
            if validator.is_valid(decimal_example):
                raise ValueError(f"{schema_name} accepted decimal {money_field}")

        if example_name.endswith("order-placed.v1.json"):
            total = example["data"]["total_amount_minor"]
            item_total = sum(item["subtotal_minor"] for item in example["data"]["items"])
            if total != item_total:
                raise ValueError("order total does not equal item subtotals")

        print(f"valid: {example_path.relative_to(CONTRACTS_DIR.parent)}")


if __name__ == "__main__":
    main()
