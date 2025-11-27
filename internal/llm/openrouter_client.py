from __future__ import annotations

import asyncio
import json
import os
import sys
from dataclasses import dataclass
from importlib import util as importlib_util
from pathlib import Path
from typing import Any, Dict, Iterable, List, Optional, get_type_hints, get_origin, get_args
import inspect

import requests
from dotenv import load_dotenv


load_dotenv()


BASE_URL = os.getenv("OPENROUTER_BASE_URL", "https://openrouter.ai/api/v1")
MODEL = os.getenv("OPENROUTER_MODEL", "x-ai/grok-4.1-fast")
API_KEY = os.getenv("OPENROUTER_API_KEY")

# API_KEY = "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"
SYSTEM_PROMPT = """You are an MCP client that calls tools efficiently. You can only use the MCP tools, you can't do a web search.

CRITICAL RULES:
1. When searching for products by description, features, or categories (e.g., "watch with GPS", "italian food", "wireless headphones"), ALWAYS use search_merchant_items FIRST. This uses semantic search (RAG) to find relevant items quickly.
2. Only use list_merchant_items if you need to browse a specific merchant's complete catalog after you know their merchant_id.
3. Do NOT guess merchant IDs - use list_merchants first if you need to discover available merchants.
4. Avoid making multiple redundant tool calls - use the most efficient tool for the task."""
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


def _python_type_to_json_schema(py_type: Any) -> Dict[str, Any]:
    """Convert Python type hints to JSON Schema."""
    origin = get_origin(py_type)
    args = get_args(py_type)

    if py_type is None:
        return {"type": "null"}
    
    # Handle basic types
    if py_type == str:
        return {"type": "string"}
    elif py_type == int:
        return {"type": "integer"}
    elif py_type == float:
        return {"type": "number"}
    elif py_type == bool:
        return {"type": "boolean"}
    
    # Handle List/Iterable
    if origin in (list, List, Iterable) or str(py_type).startswith('typing.List'):
        item_schema = {}
        if args:
            item_schema = _python_type_to_json_schema(args[0])
        return {"type": "array", "items": item_schema}
    
    # Handle Dict
    if origin in (dict, Dict) or str(py_type).startswith('typing.Dict'):
        return {"type": "object"}
        
    # Fallback for string matching (legacy support)
    type_str = str(py_type)
    if 'str' in type_str and 'List' not in type_str and 'Dict' not in type_str:
        return {"type": "string"}
    elif 'int' in type_str and 'List' not in type_str:
        return {"type": "integer"}
        
    return {"type": "string"}  # Default fallback


def _discover_tools_from_mcp_server() -> tuple[List[Dict[str, Any]], Dict[str, Any], Optional[BaseException]]:
    """
    Auto-discover tools from mcpserver.py by finding all async functions.
    
    Returns:
        - List of OpenAI-style tool definitions
        - Dict mapping tool names to handler functions
        - Optional import error
    """
    if not MCP_IMPLEMENTATION_PATH.exists():
        return [], {}, FileNotFoundError(f"Missing MCP implementation at {MCP_IMPLEMENTATION_PATH}")

    try:
        spec = importlib_util.spec_from_file_location("order_processing_mcp", MCP_IMPLEMENTATION_PATH)
        if spec is None or spec.loader is None:
            raise ImportError("Unable to load MCP implementation module spec")
        module = importlib_util.module_from_spec(spec)
        spec.loader.exec_module(module)
    except BaseException as exc:
        return [], {}, exc

    tools = []
    handlers = {}
    
    # Discover all async functions (MCP tools)
    for name, obj in inspect.getmembers(module):
        if not inspect.iscoroutinefunction(obj):
            continue
        
        # Skip private functions
        if name.startswith('_'):
            continue
            
        # Get function signature and docstring
        sig = inspect.signature(obj)
        doc = inspect.getdoc(obj) or f"Execute {name}"
        
        # Build parameter schema from function signature
        properties = {}
        required = []
        
        try:
            type_hints = get_type_hints(obj)
        except:
            type_hints = {}
        
        for param_name, param in sig.parameters.items():
            # Skip self, cls, and special parameters
            if param_name in ('self', 'cls', 'args', 'kwargs'):
                continue
                
            param_schema = _python_type_to_json_schema(type_hints.get(param_name, str))
            param_schema["description"] = f"The {param_name} parameter"
            properties[param_name] = param_schema
            
            # Mark as required if no default value
            if param.default == inspect.Parameter.empty:
                required.append(param_name)
        
        # Create OpenAI-style tool definition
        tool_def = {
            "type": "function",
            "function": {
                "name": name,
                "description": doc if doc else f"Execute {name}",
                "parameters": {
                    "type": "object",
                    "properties": properties,
                    "required": required
                }
            }
        }
        
        tools.append(tool_def)
        handlers[name] = obj
    
    return tools, handlers, None


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
    # Auto-discover tools from mcpserver.py
    tools, handlers, import_error = _discover_tools_from_mcp_server()
    
    if import_error:
        print(f"Warning: Failed to load MCP tools: {import_error}")
        print("Continuing with no tools available...")
        tools = []
        handlers = {}

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
