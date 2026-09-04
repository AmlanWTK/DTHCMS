'use client';

import { useEffect, useState } from 'react';
import { useTranslations } from 'next-intl';

import {
  readCurves,
  readGrowth,
  type GrowthCurveSet,
  type GrowthResponse,
  type Indicator,
} from '../api/growth';

import { GrowthChart } from './GrowthChart';
import { PercentileCard } from './PercentileCard';

/**
 * The card and the chart together (CP48).
 *
 * The card first, because it is the answer; the chart beneath, because it is the evidence.
 * A screen that opened with a chart would make a physician read a line before reading a
 * number, which is the wrong way round when a child is sitting in front of them.
 *
 * The indicator tabs default to **BMI-for-age**, not height. Height-for-age is the chart
 * every paediatric textbook opens with, and it is the wrong default for this clinic: obesity
 * is the largest single presenting problem here, and [R-06]'s flag is read off BMI.
 */
const INDICATORS: Indicator[] = ['BFA', 'HFA', 'WFA'];

export function GrowthScreen({ patientId }: { patientId: string }) {
  const t = useTranslations('growth');
  const [data, setData] = useState<GrowthResponse | null>(null);
  const [curves, setCurves] = useState<Record<string, GrowthCurveSet>>({});
  const [indicator, setIndicator] = useState<Indicator>('BFA');
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    let live = true;
    readGrowth(patientId)
      .then((result) => {
        if (live) setData(result);
      })
      .catch(() => {
        if (live) setFailed(true);
      });
    return () => {
      live = false;
    };
  }, [patientId]);

  const sex = data?.growth.sex;
  useEffect(() => {
    if (sex !== 'male' && sex !== 'female') return;
    if (curves[indicator] !== undefined) return;
    let live = true;
    readCurves(indicator, sex)
      .then((set) => {
        if (live) setCurves((current) => ({ ...current, [indicator]: set }));
      })
      .catch(() => undefined);
    return () => {
      live = false;
    };
  }, [indicator, sex, curves]);

  if (failed) return <p className="app-empty">{t('unavailable')}</p>;
  if (data === null) return <p className="app-empty">{t('loading')}</p>;

  const points = data.growth.history?.[indicator] ?? [];
  const set = curves[indicator];

  return (
    <div className="app-stack">
      <PercentileCard growth={data.growth} weightStatus={data.weight_status} />

      {data.growth.applicable ? (
        <section className="app-stack" aria-label={t('chartSection')}>
          <div className="app-growth__tabs" role="tablist">
            {INDICATORS.map((candidate) => (
              <button
                key={candidate}
                type="button"
                role="tab"
                aria-selected={candidate === indicator}
                data-testid={`growth-tab-${candidate}`}
                className="app-growth__tab"
                onClick={() => setIndicator(candidate)}
              >
                {t(`indicator.${candidate}`)}
              </button>
            ))}
          </div>

          {set === undefined ? (
            <p className="app-empty">{t('loading')}</p>
          ) : (
            <GrowthChart
              curves={set}
              points={points}
              indicator={indicator}
              caption={t('caption', {
                standards: set.standards.map((standard) => t(`standard.${standard.code}`)).join(' · '),
              })}
            />
          )}
        </section>
      ) : null}
    </div>
  );
}
