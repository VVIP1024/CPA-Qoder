#!/usr/bin/env python3
"""Local CLI protocol fixture for smoke.py --fixture. No network or credentials."""
import json
import os
from pathlib import Path
import sys
import time

args = sys.argv
config = Path(args[args.index('--config-dir')+1])
if '--list-models' in args:
    model = 'Auto' if config.name == 'global' else 'Qwen3.8-Flash'
    print(f'MODEL\n{model}')
    sys.exit(0)
(config/'cli.pid').write_text(str(os.getpid()))
prompt = sys.stdin.read()
if 'long detailed story' in prompt:
    time.sleep(60)

def event(value):
    print(json.dumps({'type': 'stream_event', 'event': value}), flush=True)

mcp = json.loads(args[args.index('--mcp-config')+1])
if mcp['mcpServers']:
    tools = json.loads(Path(mcp['mcpServers']['cpa']['args'][1]).read_text())
    for i, tool in enumerate(tools):
        event({'type': 'content_block_start', 'index': i, 'content_block': {
            'type': 'tool_use', 'id': f'call_{i}', 'name': 'mcp__cpa__'+tool['name'], 'input': {}}})
        if tool['name'] == 'weather':
            event({'type': 'content_block_delta', 'index': i, 'delta': {
                'type': 'input_json_delta', 'partial_json': '{"city":"Beijing"}'}})
    event({'type': 'message_delta', 'delta': {'stop_reason': 'tool_use'}})
    time.sleep(60)
text = 'Sunny' if 'Sunny' in prompt else 'OK'
# More records than CPA's sixteen-slot executor queue; content is still "OK".
for _ in range(64):
    event({'type': 'content_block_delta', 'index': 0, 'delta': {'type': 'thinking_delta', 'thinking': 'r'}})
for char in text:
    event({'type': 'content_block_delta', 'index': 1, 'delta': {'type': 'text_delta', 'text': char}})
print(json.dumps({'type': 'result', 'subtype': 'success', 'is_error': False,
    'result': text, 'stop_reason': 'end_turn', 'usage': {'input_tokens': 4, 'output_tokens': 64}}), flush=True)
