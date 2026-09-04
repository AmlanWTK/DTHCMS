"""Build core.growth_lms seed SQL + the validation fixture from the published tables."""
import csv, json, math, os, sys

WHO = '/tmp/gd/pygrowup-0.8.2/pygrowup/tables/'
CDC = '/tmp/gd/pygrowup-0.8.2/pygrowup/tables/source/cdc/'

lms = []      # (standard, indicator, sex, age_months, L, M, S)
cut = {}      # standard -> indicator -> sex -> [[age, v...], ...]

def add_cut(std, ind, sex, row):
    cut.setdefault(std, {}).setdefault(ind, {}).setdefault(sex, []).append(row)

# ---------- WHO 2006, 0-60 months ----------
WHO_Z = [('SD3neg',-3),('SD2neg',-2),('SD1neg',-1),('SD0',0),('SD1',1),('SD2',2),('SD3',3)]

def who_rows(stem, sex, indicator, lo, hi, prefer_second_at=None):
    d = json.load(open(WHO + stem + '_zscores.json'))
    seen = {}
    for r in d:
        m = int(r['Month'])
        if m < lo or m > hi: continue
        if m in seen and prefer_second_at is not None and m == prefer_second_at:
            seen[m] = r            # standing height wins at 24 months
        elif m not in seen:
            seen[m] = r
    for m in sorted(seen):
        r = seen[m]
        lms.append(('WHO_2006', indicator, sex, float(m), float(r['L']), float(r['M']), float(r['S'])))
        add_cut('WHO_2006', indicator, sex, [m] + [float(r[k]) for k, _ in WHO_Z])

for sex, tag in (('male','boys'), ('female','girls')):
    who_rows(f'lhfa_{tag}_0_5', sex, 'HFA', 0, 60, prefer_second_at=24)
    who_rows(f'wfa_{tag}_0_5',  sex, 'WFA', 0, 60)
    who_rows(f'bmifa_{tag}_0_2', sex, 'BFA', 0, 23)
    who_rows(f'bmifa_{tag}_2_5', sex, 'BFA', 24, 60)

# ---------- CDC 2000, from 60 months ----------
CDC_P = [('P3',3),('P5',5),('P10',10),('P25',25),('P50',50),('P75',75),('P85',85),('P90',90),('P95',95),('P97',97)]
cdc_columns = {}
CDC_FILES = {'statage.csv':'HFA', 'wtage.csv':'WFA', 'bmiage.csv':'BFA'}

for fn, indicator in CDC_FILES.items():
    for r in csv.DictReader(open(CDC + fn)):
        age = float(r['Agemos'])
        if age < 60: continue
        sex = 'male' if r['Sex'].strip() == '1' else 'female'
        lms.append(('CDC_2000', indicator, sex, age, float(r['L']), float(r['M']), float(r['S'])))
        present = [(k, p) for k, p in CDC_P if r.get(k, '').strip() not in ('', '.')]
        cdc_columns.setdefault(indicator, [p for _, p in present])
        add_cut('CDC_2000', indicator, sex, [age] + [float(r[k]) for k, _ in present])

lms.sort(key=lambda x: (x[0], x[1], x[2], x[3]))
print('lms rows:', len(lms))
for std in cut:
    for ind in cut[std]:
        for sex in cut[std][ind]:
            cut[std][ind][sex].sort(key=lambda r: r[0])
print('cut rows:', sum(len(v) for s in cut.values() for i in s.values() for v in i.values()))

def g(x):
    s = repr(float(x))
    return s

with open('/tmp/growth/lms.sql', 'w') as f:
    f.write("INSERT INTO core.growth_lms (standard_code, indicator, sex, age_months, l, m, s) VALUES\n")
    f.write(",\n".join(
        f"  ('{a}','{b}','{c}',{d:g},{g(e)},{g(m)},{g(s)})" for a,b,c,d,e,m,s in lms))
    f.write("\nON CONFLICT (standard_code, indicator, sex, age_months) DO UPDATE\n"
            "  SET l = EXCLUDED.l, m = EXCLUDED.m, s = EXCLUDED.s;\n")

fixture = {
    "_": "Published cut-offs from the reference tables themselves. Every value here is what "
         "WHO or CDC printed; the engine must reproduce it from the seeded L, M and S.",
    "who_z": [z for _, z in WHO_Z],
    "cdc_p": cdc_columns,
    "tables": cut,
}
# Minified on purpose: Prettier owns the committed shape of this file, so anything this
# script chose here would be overwritten. See README.md — run Prettier over it afterwards.
json.dump(fixture, open('/tmp/growth/growth-reference.json','w'), separators=(',',':'))
print('sql bytes', os.path.getsize('/tmp/growth/lms.sql'))
print('fixture bytes', os.path.getsize('/tmp/growth/growth-reference.json'))
