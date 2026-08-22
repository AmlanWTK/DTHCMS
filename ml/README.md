# `ml/` — Python ML and OCR service

The only service written in a language other than Go, because the OCR, image
preprocessing and predictive-model ecosystem is Python. It is deployed separately from
the backend so that a burst of document processing can never slow clinical data capture.

- Image preprocessing — **CP99**
- OCR integration — **CP101**
- Table and entity extraction — **CP103, CP104**
- Predictive models (no-show risk, procurement forecasting) — **CP134, CP142**

Empty until CP99.
