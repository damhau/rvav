# cmdb

A stand-in for a CMDB's backup policies, for the S8 stack test of a REST
value source (`web/e2e-stack/catalog-admin.spec.ts`). nginx serves this
directory on the stack's network:

```sh
docker run -d --rm --name rvav-cmdb --network rvav_default \
  -v "$PWD/deploy/cmdb:/usr/share/nginx/html:ro" nginx:1.27-alpine
RVAV_REST_SOURCE_HOSTS=http://rvav-cmdb make up   # plain http, for this stand-in only
cd web && RVAV_STACK_CMDB=1 RVAV_WEB_URL=http://localhost:3000 pnpm e2e:stack
```
