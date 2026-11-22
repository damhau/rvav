/*
 * Script Include: RvavClient (global, not client callable).
 *
 * Calls rvav's ServiceNow endpoints (/api/v1/servicenow/, ADR-0065) with
 * the token of the Connection & Credential alias named by the system
 * property rvav.alias. Every method answers {status, body, retryAfter}:
 * status 0 means rvav couldn't be reached.
 */
var RvavClient = Class.create();

RvavClient.prototype = {
  initialize: function () {
    var alias = gs.getProperty('rvav.alias', 'global.rvav');
    var info = new sn_cc.ConnectionInfoProvider().getConnectionInfo(alias);
    if (info == null) {
      throw new Error('rvav: no connection alias ' + alias + ' (system property rvav.alias)');
    }
    this.url = String(info.getAttribute('connection_url') || '').replace(/\/+$/, '');
    this.token = String(info.getCredentialAttribute('api_key') || '');
    this.midServer = gs.getProperty('rvav.mid_server', '');
    if (this.url.indexOf('https://') !== 0 || this.token === '') {
      throw new Error('rvav: the alias ' + alias + ' needs an https URL and an API key credential');
    }
  },

  catalog: function () {
    return this._call('get', '/catalog');
  },

  entitlements: function (user) {
    return this._call('get', '/entitlements?user=' + encodeURIComponent(user));
  },

  submit: function (draft) {
    return this._call('post', '/requests', draft);
  },

  request: function (ritmSysId) {
    return this._call('get', '/requests/' + encodeURIComponent(ritmSysId));
  },

  _call: function (method, path, body) {
    var rm = new sn_ws.RESTMessageV2();
    rm.setEndpoint(this.url + '/api/v1/servicenow' + path);
    rm.setHttpMethod(method);
    rm.setRequestHeader('Authorization', 'Bearer ' + this.token);
    rm.setRequestHeader('Accept', 'application/json');
    rm.setHttpTimeout(30000);
    if (body !== undefined) {
      rm.setRequestHeader('Content-Type', 'application/json');
      rm.setRequestBody(JSON.stringify(body));
    }
    if (this.midServer) {
      rm.setMIDServer(this.midServer);
    }
    var res;
    try {
      res = rm.execute();
      if (this.midServer) {
        res.waitForResponse(60);
      }
    } catch (e) {
      return { status: 0, body: null, error: String(e) };
    }
    // Java values from the Glide API, made JavaScript ones for === to work.
    var out = { status: Number(res.getStatusCode()), body: null,
      retryAfter: parseInt(String(res.getHeader('Retry-After') || ''), 10) || 0 };
    if (res.haveError() && out.status === 0) {
      out.error = String(res.getErrorMessage());
      return out;
    }
    try {
      out.body = JSON.parse(String(res.getBody() || 'null'));
    } catch (e) {
      out.body = null;
    }
    return out;
  },

  type: 'RvavClient'
};

RvavClient.problem = function (res) {
  if (res.status === 0) {
    return 'rvav could not be reached: ' + (res.error || 'no answer');
  }
  var p = res.body || {};
  var text = (p.title || 'HTTP ' + res.status) + (p.detail ? ': ' + p.detail : '');
  var errors = p.errors || [];
  for (var i = 0; i < errors.length; i++) {
    text += '\n- ' + (errors[i].field ? errors[i].field + ': ' : '') + errors[i].message;
  }
  if (p.request_id) {
    text += '\n(rvav request ID ' + p.request_id + ')';
  }
  return text;
};
