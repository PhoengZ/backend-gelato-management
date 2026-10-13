"""
SSE Listener for GelatoFlow Real-Time Notifications.
Reads unbuffered Server-Sent Events from /api/v1/notifications/stream in a background thread.
"""

import json
import logging
import queue
import threading
import time
from typing import Any, Dict, List, Optional

import requests

logger = logging.getLogger(__name__)


class SSEListener:
    """
    Subscribes to GelatoFlow SSE stream asynchronously.
    Pushes received events to an internal thread-safe queue.
    """

    def __init__(self, base_url: str, token: str, timeout: float = 30.0):
        self.url = f"{base_url.rstrip('/')}/api/v1/notifications/stream"
        self.token = token
        self.timeout = timeout
        self.event_queue: queue.Queue = queue.Queue()
        self.all_events: List[Dict[str, Any]] = []
        self._stop_event = threading.Event()
        self._thread: Optional[threading.Thread] = None
        self.connected = False
        self.error: Optional[Exception] = None

    def start(self) -> "SSEListener":
        """Starts background streaming thread."""
        self._stop_event.clear()
        self._thread = threading.Thread(target=self._run, daemon=True)
        self._thread.start()
        # Brief pause to let thread establish connection
        time.sleep(0.1)
        return self

    def stop(self) -> None:
        """Signals stop and waits for thread termination."""
        self._stop_event.set()
        if self._thread and self._thread.is_alive():
            self._thread.join(timeout=2.0)

    def _run(self) -> None:
        headers = {
            "Accept": "text/event-stream",
            "Authorization": f"Bearer {self.token}",
        }
        try:
            with requests.get(self.url, headers=headers, stream=True, timeout=self.timeout) as response:
                if response.status_code != 200:
                    logger.warning("SSE connection returned HTTP %d", response.status_code)
                    return

                self.connected = True
                current_id = None
                current_event = None
                current_data_lines = []

                for raw_line in response.iter_lines(decode_unicode=True):
                    if self._stop_event.is_set():
                        break

                    if raw_line is None:
                        continue

                    line = raw_line.strip()
                    if not line:
                        # Empty line signals dispatch of current SSE frame
                        if current_data_lines or current_event:
                            data_str = "\n".join(current_data_lines)
                            parsed_data = None
                            try:
                                parsed_data = json.loads(data_str) if data_str else {}
                            except json.JSONDecodeError:
                                parsed_data = {"raw": data_str}

                            frame = {
                                "id": current_id,
                                "event": current_event,
                                "data": parsed_data,
                                "received_at": time.time(),
                            }
                            self.all_events.append(frame)
                            self.event_queue.put(frame)

                        current_id = None
                        current_event = None
                        current_data_lines = []
                        continue

                    if line.startswith(":"):
                        # SSE comment line (e.g. : keep-alive)
                        continue

                    if line.startswith("id:"):
                        current_id = line[3:].strip()
                    elif line.startswith("event:"):
                        current_event = line[6:].strip()
                    elif line.startswith("data:"):
                        current_data_lines.append(line[5:].strip())

        except Exception as e:
            self.error = e
            logger.debug("SSE listener stopped: %v", e)
        finally:
            self.connected = False

    def wait_for_event(self, event_type: str, timeout: float = 10.0) -> Optional[Dict[str, Any]]:
        """
        Polls queue until an event matching event_type arrives or timeout occurs.
        """
        deadline = time.time() + timeout
        while time.time() < deadline:
            remaining = max(0.1, deadline - time.time())
            try:
                frame = self.event_queue.get(timeout=remaining)
                if frame.get("event") == event_type:
                    return frame
            except queue.Empty:
                break
        return None
