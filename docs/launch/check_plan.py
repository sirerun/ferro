#!/usr/bin/env python3
"""Validate the launch DAG and render bounded task cards; stdlib only."""
import argparse
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parent

def render(tasks, waves):
    lines = ['# Executable task cards', '', 'Generated from tasks.json; change that file and run `python3 docs/launch/check_plan.py --write`.', '', 'All listed checks are required future verification, not claims that they already ran. Read agent-runbook.md before assigning work.', '']
    lines += ['## Dependency waves', '', 'A wave is a dependency frontier, not permission to run every task simultaneously. Enforce path ownership and the build lease; use at most four initial lanes.', '']
    for i, wave in enumerate(waves):
        lines += [f'- Wave {i}: '+', '.join(wave)]
    lines += ['']
    for task in tasks:
        lines += [f'## {task["id"]} — {task["title"]}', '', f'- Repository: `{task["repository"]}`; lane: `{task["lane"]}`.', f'- Dependencies: {", ".join(task["depends_on"]) or "none"}.', f'- Owned paths: {", ".join("`"+p+"`" for p in task["owns"])}.', f'- Reviewer: {task["reviewer"]}; initial size: {task["estimated_agent_hours"]} agent hours; status: {task["status"]}.', '', 'Implementation:', '']
        lines += [f'{n}. {s}' for n,s in enumerate(task['steps'],1)]
        lines += ['', 'Acceptance:', ''] + ['- '+s for s in task['acceptance']]
        lines += ['', '**Required verification:** '+task['verification'], '']
    return '\n'.join(lines)+'\n'

def main():
    parser=argparse.ArgumentParser();parser.add_argument('--write',action='store_true');args=parser.parse_args()
    manifest=json.loads((ROOT/'tasks.json').read_text()); tasks=manifest['tasks']
    ids=[t['id'] for t in tasks];assert len(ids)==len(set(ids)), 'duplicate IDs'
    known=set(ids)
    for t in tasks:
        for field in ['repository','lane','owns','steps','acceptance','verification','reviewer','status']:
            assert t.get(field), f'{t["id"]}: missing {field}'
        assert len(t['depends_on'])==len(set(t['depends_on'])), 'duplicate dependency'
        assert set(t['depends_on']) <= known, f'{t["id"]}: unknown dependencies'
        assert t['id'] not in t['depends_on'], 'self dependency'
        assert t['status'] in {'planned','ready','in_progress','review','integrated','consumer_verified','blocked'}, 'bad status'
        assert all(not p.startswith('/') and '..' not in Path(p).parts for p in t['owns']), 'unsafe path scope'
    done=set();waves=[]
    while len(done)<len(tasks):
        ready=[t['id'] for t in tasks if t['id'] not in done and set(t['depends_on'])<=done]
        assert ready, 'dependency cycle'
        waves.append(ready);done.update(ready)
    byid={t['id']:t for t in tasks}
    ancestors=set()
    def visit(id):
        if id in ancestors:return
        ancestors.add(id)
        for dep in byid[id]['depends_on']:visit(dep)
    visit('R04')
    assert ancestors==known, 'orphan work not included in release gate: '+str(known-ancestors)
    # Report potential same-wave directory overlap; coordinator must serialize it.
    overlaps=[]
    for wave in waves:
        for i,a in enumerate(wave):
            for b in wave[i+1:]:
                if byid[a]['repository']!=byid[b]['repository']:continue
                for pa in byid[a]['owns']:
                    for pb in byid[b]['owns']:
                        if pa==pb or pa.startswith(pb.rstrip('/')+'/') or pb.startswith(pa.rstrip('/')+'/'):
                            overlaps.append((a,b,pa,pb))
    assert not overlaps, 'same-wave ownership overlap: '+str(overlaps)
    output=render(tasks,waves);target=ROOT/'task-cards.md'
    if args.write:target.write_text(output)
    else:assert target.exists() and target.read_text()==output, 'task-cards.md stale; run with --write'
    print(f'PASS: {len(tasks)} tasks, {len(waves)} dependency frontiers, no cycles/orphans/same-wave path collisions; task cards match manifest.')

if __name__=='__main__':main()
