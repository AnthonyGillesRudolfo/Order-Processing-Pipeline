from __future__ import annotations

import asyncio
import json
import os
import sys
from dataclasses import dataclass
from importlib import util as importlib_util
from pathlib import Path
from typing import Any, Dict, Iterable, List, Optional
import inspect

import requests
from dotenv import load_dotenv


load_dotenv()


class OpenBaoSecretNotFound(RuntimeError):
    """Raised when the requested OpenBao path does not exist."""


def _bootstrap_from_openbao() -> None:
    """
    Fetch secrets from an OpenBao KV v2 path (if configured) and load them into the environment.

    Mirrors the Go helpers so local tooling can rely on the same source of truth.
    """

    addr = (os.getenv("OPENBAO_ADDR") or "http://127.0.0.1:8200").strip().rstrip("/")
    token = os.getenv("OPENBAO_TOKEN") or "dev-root-token"
    secret_path = (os.getenv("OPENBAO_SECRET_PATH") or "order-processing/dev").strip().strip("/")

    if not (addr and token and secret_path):
        return

    mount = (os.getenv("OPENBAO_MOUNT") or "secret").strip().strip("/")
    namespace = (os.getenv("OPENBAO_NAMESPACE") or "").strip()

    url = f"{addr}/v1/{mount}/data/{secret_path}"
    headers = {"X-Vault-Token": token}
    if namespace:
        headers["X-Vault-Namespace"] = namespace

    try:
        response = requests.get(url, headers=headers, timeout=5)
    except requests.RequestException as exc:
        raise RuntimeError(f"call OpenBao at {url} failed: {exc}") from exc

    if response.status_code == 404:
        raise OpenBaoSecretNotFound(f"OpenBao path not found: {mount}/data/{secret_path}")
    response.raise_for_status()

    payload = response.json()
    data = payload.get("data", {}).get("data", {})
    for key, value in data.items():
        if isinstance(value, (dict, list)):
            continue  # keep behaviour simple; only flat envs are expected
        os.environ[key] = str(value)


try:
    _bootstrap_from_openbao()
except OpenBaoSecretNotFound as exc:
    print(f"[OpenBao] {exc}", file=sys.stderr)
except RuntimeError as exc:
    print(f"[OpenBao] bootstrap failed: {exc}", file=sys.stderr)


BASE_URL = os.getenv("OPENROUTER_BASE_URL", "https://openrouter.ai/api/v1")
MODEL = os.getenv("OPENROUTER_MODEL", "minimax/minimax-m2:free")
API_KEY = os.getenv("OPENROUTER_API_KEY")
SYSTEM_PROMPT = "You are an MCP client that calls tools. You can only use the MCP tools, you can't do a web search."
MANIFEST_PATH = Path("mcp-server/manifest.json")
MCP_IMPLEMENTATION_PATH = Path("mcp-server/mcpserver.py")


if not API_KEY:
    raise ValueError("Missing OPENROUTER_API_KEY environment variable")


HEADERS = {
    "Authorization": f"Bearer {API_KEY}",
    "HTTP-Referer": "https://your-app.example",  # optional but recommended
    "X-Title": "Order Processing MCP",
}


@dataclass
class ToolExecutionResult:
    content: str
    error: Optional[str] = None


def _load_manifest_tools(manifest: Dict[str, Any]) -> List[Dict[str, Any]]:
    """Translate the MCP manifest tools to the OpenAI-style tool schema."""
    tools = []
    for tool in manifest.get("tools", []):
        tools.append(
            {
                "type": "function",
                "function": {
                    "name": tool["name"],
                    "description": tool.get("description", ""),
                    "parameters": tool.get(
                        "input_schema",
                        {"type": "object", "properties": {}, "required": []},
                    ),
                },
            }
        )
    return tools


def _load_manifest() -> Dict[str, Any]:
    with MANIFEST_PATH.open() as fh:
        return json.load(fh)


def _prepare_tool_handlers(tool_names: Iterable[str]) -> tuple[Dict[str, Any], Optional[BaseException]]:
    """
    Attempt to import the MCP server implementation so we can execute tools locally.

    Returns a mapping of tool name -> coroutine function and any import error encountered.
    """
    if not MCP_IMPLEMENTATION_PATH.exists():
        return {}, FileNotFoundError(f"Missing MCP implementation at {MCP_IMPLEMENTATION_PATH}")

    try:
        spec = importlib_util.spec_from_file_location("order_processing_mcp", MCP_IMPLEMENTATION_PATH)
        if spec is None or spec.loader is None:
            raise ImportError("Unable to load MCP implementation module spec")
        module = importlib_util.module_from_spec(spec)
        spec.loader.exec_module(module)
    except BaseException as exc:  # pragma: no cover - import guard
        return {}, exc

    handlers: Dict[str, Any] = {}
    for name in tool_names:
        handler = getattr(module, name, None)
        if handler is not None and inspect.iscoroutinefunction(handler):
            handlers[name] = handler
    return handlers, None


def _call_openrouter(messages: List[Dict[str, Any]], tools: List[Dict[str, Any]]) -> Dict[str, Any]:
    payload = {
        "model": MODEL,
        "messages": messages,
        "tools": tools,
    }

    response = requests.post(
        f"{BASE_URL}/chat/completions",
        json=payload,
        headers=HEADERS,
        timeout=90,
    )
    response.raise_for_status()
    return response.json()["choices"][0]["message"]


def _execute_tool_call(
    tool_call: Dict[str, Any],
    handlers: Dict[str, Any],
    import_error: Optional[BaseException],
) -> ToolExecutionResult:
    function_meta = tool_call.get("function", {})
    tool_name = function_meta.get("name", "unknown_tool")
    args_json = function_meta.get("arguments", "{}")

    try:
        arguments = json.loads(args_json) if args_json else {}
    except json.JSONDecodeError as exc:
        return ToolExecutionResult(content="", error=f"Failed to decode tool arguments: {exc}")

    handler = handlers.get(tool_name)
    if handler is None:
        if import_error is not None:
            return ToolExecutionResult(
                content="",
                error=f"Tool '{tool_name}' unavailable because the MCP implementation could not be imported: {import_error}",
            )
        return ToolExecutionResult(content="", error=f"No handler found for tool '{tool_name}'")

    try:
        # Tools in the MCP implementation are async, so run them through asyncio.
        result = asyncio.run(handler(**arguments))
    except BaseException as exc:  # pragma: no cover - tool execution failure
        return ToolExecutionResult(content="", error=f"Tool '{tool_name}' failed: {exc}")

    if isinstance(result, (dict, list)):
        content = json.dumps(result, indent=2)
    else:
        content = str(result)

    return ToolExecutionResult(content=content)


def _chat_turn(
    user_input: str,
    messages: List[Dict[str, Any]],
    tools: List[Dict[str, Any]],
    handlers: Dict[str, Any],
    import_error: Optional[BaseException],
) -> None:
    messages.append({"role": "user", "content": user_input})

    while True:
        assistant_message = _call_openrouter(messages, tools)
        messages.append(assistant_message)

        content = assistant_message.get("content")
        if content:
            print(f"Assistant: {content}")

        tool_calls = assistant_message.get("tool_calls") or []
        if not tool_calls:
            break

        for tool_call in tool_calls:
            function_meta = tool_call.get("function", {})
            tool_name = function_meta.get("name", "unknown_tool")
            args_json = function_meta.get("arguments", "{}")

            try:
                args_preview = json.loads(args_json) if args_json else {}
            except json.JSONDecodeError:
                args_preview = args_json

            print(f"[Tool call] {tool_name} args={args_preview}")
            execution = _execute_tool_call(tool_call, handlers, import_error)
            if execution.error:
                print(f"[Tool error] {execution.error}")
            else:
                print(f"[Tool result] {execution.content}")

            messages.append(
                {
                    "role": "tool",
                    "tool_call_id": tool_call.get("id"),
                    "name": tool_name,
                    "content": execution.content if not execution.error else execution.error,
                }
            )


def main() -> None:
    manifest = _load_manifest()
    tools = _load_manifest_tools(manifest)
    tool_names = [tool["function"]["name"] for tool in tools]
    handlers, import_error = _prepare_tool_handlers(tool_names)

    messages: List[Dict[str, Any]] = [{"role": "system", "content": SYSTEM_PROMPT}]

    initial_input = " ".join(sys.argv[1:]).strip()
    if not initial_input:
        try:
            initial_input = input("You: ").strip()
        except (EOFError, KeyboardInterrupt):
            print()
            return

    while initial_input:
        print(f"You: {initial_input}")
        try:
            _chat_turn(initial_input, messages, tools, handlers, import_error)
        except requests.RequestException as exc:
            print(f"[Request error] {exc}")

        try:
            initial_input = input("You (enter to quit): ").strip()
        except (EOFError, KeyboardInterrupt):
            print()
            break


if __name__ == "__main__":
    main()
