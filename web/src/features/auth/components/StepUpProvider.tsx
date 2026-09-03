'use client';

import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from 'react';

import type { StepUpPurpose } from '../api/secondFactor';
import { StepUpDialog, type StepUpRequest } from './StepUpDialog';

/**
 * `useStepUp` — ask the person for a fresh second factor and get a token back.
 *
 *   const requestStepUp = useStepUp();
 *   const token = await requestStepUp('prescription.sign', 'Sign this prescription');
 *   await signPrescription(id, token);
 *
 * One dialog for the whole application, mounted once in the shell. Callers await a
 * promise; the dialog resolves it with a token or rejects it with StepUpCancelled. A caller
 * that wants to retry a refused request simply asks again.
 */

type RequestStepUp = (purpose: StepUpPurpose, description: ReactNode) => Promise<string>;

const StepUpContext = createContext<RequestStepUp | null>(null);

export function StepUpProvider({ children }: { children: ReactNode }) {
  const [request, setRequest] = useState<StepUpRequest | null>(null);

  const requestStepUp = useCallback<RequestStepUp>(
    (purpose, description) =>
      new Promise<string>((resolve, reject) => {
        setRequest({ purpose, description, resolve, reject });
      }),
    [],
  );

  const value = useMemo(() => requestStepUp, [requestStepUp]);

  return (
    <StepUpContext.Provider value={value}>
      {children}
      <StepUpDialog request={request} onClose={() => setRequest(null)} />
    </StepUpContext.Provider>
  );
}

export function useStepUp(): RequestStepUp {
  const request = useContext(StepUpContext);
  if (!request) {
    throw new Error('useStepUp must be used inside StepUpProvider — it is mounted in the shell');
  }
  return request;
}
