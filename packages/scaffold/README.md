# __NAME__

A web service packaged with karga. Install it with:

    karga install my-__NAME__ ./__NAME__

Values (see `values.yaml`): `image`, `service.port` (the port of the `web` Service on the release
network), `expose.host` and `expose.listener` (serve it on a listener of the host gateway; nothing is
exposed without a host) and `resources`.
