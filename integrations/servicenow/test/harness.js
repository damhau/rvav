'use strict';
// Runs the scripts of integrations/servicenow/ under Node, with an
// in-memory stand-in for the Glide APIs they use and rvav's answers given
// in a fixture, so that a Go end-to-end test (internal/e2e,
// TestServiceNowScripts) can check them against rvav's real API.
//
//   node harness.js fixture.json > out.json
//
// The fixture: {properties, user, responses: {"GET /catalog": {status,
// body, headers}}, steps: [{do: "import"} | {do: "projects", item} |
// {do: "order", item, number, approval, variables, requested_for,
// opened_by}]}, the two people sys_user sys_ids, the fixture's user when
// left out. The output: the
// tables, the calls made to rvav, the events queued, the log and each
// step's answer.

const fs = require('fs');
const path = require('path');
const vm = require('vm');

const fixture = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'));
const dir = path.join(__dirname, '..');
const tables = {};
const calls = [];
const events = [];
const log = [];
const answers = [];
let ids = 0;

// The references the scripts dot-walk.
const refs = { sc_req_item: { cat_item: 'sc_cat_item', requested_for: 'sys_user', opened_by: 'sys_user' } };

function rows(table) {
  tables[table] = tables[table] || [];
  return tables[table];
}

function field(table, row, name) {
  const [head, ...rest] = name.split('.');
  if (rest.length === 0) {
    return row[head];
  }
  const target = rows(refs[table][head]).find((r) => r.sys_id === row[head]);
  return target ? field(refs[table][head], target, rest.join('.')) : undefined;
}

function matches(table, row, q) {
  const v = field(table, row, q.field);
  const s = v === undefined || v === null ? '' : String(v);
  switch (q.op) {
    case '=':
      return s === String(q.value);
    case 'STARTSWITH':
      return s.indexOf(q.value) === 0;
    case 'IN':
      return String(q.value).split(',').indexOf(s) >= 0;
    case '<':
    case '>':
      return true; // dates: every row is in range
    default:
      throw new Error('no operator ' + q.op);
  }
}

class Element {
  constructor(record, name) {
    this.record = record;
    this.name = name;
  }
  toString() {
    const v = this.record._row[this.name];
    return v === undefined || v === null ? '' : String(v);
  }
  nil() {
    return this.toString() === '';
  }
  changes() {
    return (this.record._changed || []).indexOf(this.name) >= 0;
  }
  getRefRecord() {
    const gr = new GlideRecord(refs[this.record._table][this.name]);
    gr.get(this.toString());
    return gr;
  }
  getED() {
    return { getLength: () => 40 };
  }
}

function GlideRecord(table) {
  const self = { _table: table, _queries: [], _found: [], _at: -1, _row: {}, _limit: Infinity };
  const methods = {
    initialize() {
      self._row = {};
    },
    addQuery(f, op, value) {
      if (value === undefined) {
        value = op;
        op = '=';
      }
      self._queries.push({ field: f, op, value });
    },
    addActiveQuery() {
      self._queries.push({ field: 'active', op: '=', value: 'true' });
    },
    orderBy() {},
    setLimit(n) {
      self._limit = n;
    },
    query() {
      self._found = rows(table).filter((r) => self._queries.every((q) => matches(table, r, q))).slice(0, self._limit);
      self._at = -1;
    },
    next() {
      self._at++;
      if (self._at >= self._found.length) {
        return false;
      }
      self._row = self._found[self._at];
      return true;
    },
    get(a, b) {
      self._queries = b === undefined ? [{ field: 'sys_id', op: '=', value: a }] : [{ field: a, op: '=', value: b }];
      methods.query();
      return methods.next();
    },
    getValue(f) {
      const v = self._row[f];
      return v === undefined || v === null ? null : String(v);
    },
    setValue(f, v) {
      self._row[f] = v;
    },
    getUniqueValue() {
      return self._row.sys_id;
    },
    getElement(f) {
      return new Element(self, f);
    },
    insert() {
      self._row.sys_id = (++ids).toString(16).padStart(32, '0');
      rows(table).push(self._row);
      return self._row.sys_id;
    },
    update() {
      return self._row.sys_id;
    },
    deleteRecord() {
      const all = rows(table);
      all.splice(all.indexOf(self._row), 1);
    },
    operation() {
      return self._operation;
    }
  };
  return new Proxy(self, {
    get(target, prop) {
      if (prop in methods) {
        return methods[prop];
      }
      if (prop === 'variables') {
        return target._row.variables;
      }
      if (typeof prop === 'string' && !prop.startsWith('_')) {
        return new Element(target, prop);
      }
      return target[prop];
    },
    set(target, prop, value) {
      if (prop.startsWith('_')) {
        target[prop] = value;
      } else {
        target._row[prop] = value;
      }
      return true;
    }
  });
}

class RESTMessageV2 {
  constructor() {
    this.headers = {};
  }
  setEndpoint(url) {
    this.url = url;
  }
  setHttpMethod(m) {
    this.method = m.toUpperCase();
  }
  setRequestHeader(k, v) {
    this.headers[k] = v;
  }
  setRequestBody(b) {
    this.body = b;
  }
  setHttpTimeout() {}
  setMIDServer() {}
  execute() {
    const p = this.url.substring(this.url.indexOf('/api/v1/servicenow') + '/api/v1/servicenow'.length);
    calls.push({ method: this.method, path: p, authorization: this.headers.Authorization,
      body: this.body === undefined ? null : JSON.parse(this.body) });
    const r = fixture.responses[this.method + ' ' + p] || { status: 404, body: { title: 'Not found' } };
    return {
      getStatusCode: () => r.status,
      getBody: () => JSON.stringify(r.body === undefined ? null : r.body),
      getHeader: (h) => (r.headers || {})[h] || null,
      haveError: () => r.status === 0 || r.status >= 400,
      getErrorMessage: () => r.error || '',
      waitForResponse() {}
    };
  }
}

const context = vm.createContext({
  JSON,
  Proxy,
  GlideRecord,
  gs: {
    getProperty: (k, d) => ((fixture.properties || {})[k] !== undefined ? fixture.properties[k] : d),
    getUserID: () => (fixture.user ? fixture.user.sys_id : ''),
    info: (m) => log.push('info: ' + m),
    warn: (m) => log.push('warn: ' + m),
    error: (m) => log.push('error: ' + m),
    minutesAgo: () => '',
    daysAgo: () => '',
    eventQueue: (name, gr, p1, p2) => events.push({ name, sys_id: gr.getUniqueValue(), parm1: p1, parm2: p2 })
  },
  sn_cc: {
    ConnectionInfoProvider: class {
      getConnectionInfo(alias) {
        const c = (fixture.aliases || {})[alias];
        return c ? { getAttribute: (k) => c[k], getCredentialAttribute: (k) => c[k] } : null;
      }
    }
  },
  sn_ws: { RESTMessageV2 },
  Class: {
    create: () =>
      function () {
        this.initialize.apply(this, arguments);
      }
  },
  AbstractAjaxProcessor: function () {}
});
vm.runInContext(
  'Object.extendsObject = function (base, props) { var o = Object.create(base.prototype); ' +
    'for (var k in props) { o[k] = props[k]; } return o; };',
  context
);
context.AbstractAjaxProcessor.prototype = {
  initialize(params) {
    this.params = params || {};
  },
  getParameter(k) {
    return this.params[k];
  }
};

for (const f of ['RvavClient', 'RvavOrder', 'RvavCatalogImport', 'RvavAjax']) {
  vm.runInContext(fs.readFileSync(path.join(dir, 'script-includes', f + '.js'), 'utf8'), context, { filename: f + '.js' });
}

function itemFor(id) {
  return rows('sc_cat_item').find((r) => r.meta === 'rvav:' + id);
}

function run(file, current, operation, changed) {
  const gr = new GlideRecord('sc_req_item');
  gr.get(current);
  gr._operation = operation;
  gr._changed = changed;
  context.current = gr;
  context.previous = null;
  vm.runInContext(fs.readFileSync(path.join(dir, file), 'utf8'), context, { filename: file });
}

for (const [t, rs] of Object.entries(fixture.tables || {})) {
  rows(t).push(...rs);
}
if (fixture.user) {
  rows('sys_user').push(Object.assign({}, fixture.user));
}
for (const step of fixture.steps) {
  switch (step.do) {
    case 'import':
      vm.runInContext('new RvavCatalogImport().run()', context);
      answers.push(null);
      break;
    case 'projects': {
      const ajax = new context.RvavAjax({ sysparm_item: (itemFor(step.item) || {}).sys_id });
      answers.push(JSON.parse(ajax.projects()));
      break;
    }
    case 'order': {
      const item = itemFor(step.item);
      const ritm = new GlideRecord('sc_req_item');
      ritm.initialize();
      ritm.setValue('number', step.number);
      ritm.setValue('cat_item', item.sys_id);
      ritm.setValue('requested_for', step.requested_for || fixture.user.sys_id);
      ritm.setValue('opened_by', step.opened_by || fixture.user.sys_id);
      ritm.setValue('approval', step.approval || 'not requested');
      ritm.setValue('state', '1');
      ritm.setValue('active', true);
      ritm.setValue('variables', step.variables || {});
      const id = ritm.insert();
      const queued = events.length;
      run('business-rules/rvav_order.js', id, 'insert', []);
      if (events.length > queued) {
        run('script-actions/rvav_submit.js', id, 'update', []);
      }
      answers.push(rows('sc_req_item').find((r) => r.sys_id === id));
      break;
    }
    case 'retry':
      vm.runInContext('new RvavOrder().retry()', context);
      answers.push(null);
      break;
    default:
      throw new Error('no step ' + step.do);
  }
}

process.stdout.write(JSON.stringify({ tables, calls, events, log, answers }));
