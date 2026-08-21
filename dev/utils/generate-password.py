import os
import hashlib
import base64

def generate_rabbitmq_password_hash(password: str) -> str:
    salt = os.urandom(4)
    hash_value = hashlib.sha256(salt + password.encode('utf-8')).digest()
    password_hash = base64.b64encode(salt + hash_value).decode('utf-8')
    return password_hash

plain_password = "xxxx"
print("password_hash:", generate_rabbitmq_password_hash(plain_password))