#!/usr/bin/env python3

import json
from pathlib import Path

from jsonschema import Draft202012Validator, FormatChecker


CONTRACTS_DIR = Path(__file__).resolve().parents[1]


def load_json(path: Path) -> object:
    with path.open(encoding="utf-8") as source:
        return json.load(source)


def main() -> None:
    schema_path = CONTRACTS_DIR / "events" / "inventory-waste.v1.schema.json"
    example_path = CONTRACTS_DIR / "examples" / "inventory-waste.v1.json"

    schema = load_json(schema_path)
    example = load_json(example_path)

    Draft202012Validator.check_schema(schema)
    validator = Draft202012Validator(schema, format_checker=FormatChecker())
    validator.validate(example)

    print(f"valid: {example_path.relative_to(CONTRACTS_DIR.parent)}")


if __name__ == "__main__":
    main()
