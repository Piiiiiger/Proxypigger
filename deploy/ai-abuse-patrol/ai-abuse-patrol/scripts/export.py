#!/usr/bin/env python3
"""Fixed, bounded SSH export for the panel's read-only patrol identity."""
import json
import os
from pathlib import Path
import re
import stat
import sys

ROOT = '/var/lib/ai-abuse-patrol'


def read_json(directory, name, limit):
    fd = os.open(name, os.O_RDONLY | os.O_NOFOLLOW, dir_fd=directory)
    try:
        info = os.fstat(fd)
        if not stat.S_ISREG(info.st_mode) or info.st_size > limit:
            raise ValueError('invalid patrol artifact')
        with os.fdopen(fd, 'rb', closefd=False) as source:
            return json.load(source)
    finally:
        os.close(fd)


def export():
    if os.environ.get('SSH_ORIGINAL_COMMAND') != 'ai-patrol-export-v2':
        raise ValueError('only the patrol export command is allowed')
    root = os.open(ROOT, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        status = read_json(root, 'latest-status.json', 131072)
        if status.get('status') != 'reviewed':
            return {'version': 2, 'empty': True}
        report = status['report']
        match = re.fullmatch(re.escape(ROOT) + r'/runs/([0-9]{8}T[0-9]{12}Z)/report\.md', report)
        if not match:
            raise ValueError('invalid report location')
        runs = os.open('runs', os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=root)
        try:
            run = os.open(match[1], os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=runs)
            try:
                return {'version': 2, 'run_id': match[1],
                        'report': read_json(run, 'report.json', 131072),
                        'evidence': read_json(run, 'evidence.json', 524288),
                        'identities': read_json(run, 'identities.json', 131072)}
            finally:
                os.close(run)
        finally:
            os.close(runs)
    finally:
        os.close(root)


if __name__ == '__main__':
    try:
        print(json.dumps(export(), ensure_ascii=False, separators=(',', ':')))
    except Exception:
        print('patrol export unavailable', file=sys.stderr)
        sys.exit(1)
