/*
 * Script Include: RvavCatalogImport (global, not client callable).
 *
 * Makes a catalog item of each item rvav offers (GET /servicenow/catalog),
 * keeps it up to date, and deactivates the ones rvav no longer offers. An
 * item keeps rvav's item ID in its meta field (rvav:itm_...). Each has the
 * variable set "rvav" (project, environment, deployment name,
 * justification) and a variable rvav_in_<name> for each input rvav lists:
 * a select box for a fixed list, a checkbox for a boolean, else a line of
 * text. An input whose options rvav finds at request time (dynamic, a
 * vSphere cluster for example) is a line of text too, which rvav checks
 * when the request is submitted.
 */
var RvavCatalogImport = Class.create();

RvavCatalogImport.SET = 'rvav';

// item_option_new types.
RvavCatalogImport.MULTI_LINE = '2';
RvavCatalogImport.SELECT_BOX = '5';
RvavCatalogImport.SINGLE_LINE = '6';
RvavCatalogImport.CHECKBOX = '7';

RvavCatalogImport.save = function (gr, found) {
  if (!found) {
    return gr.insert();
  }
  gr.update();
  return gr.getUniqueValue();
};

RvavCatalogImport.prototype = {
  initialize: function (client) {
    this.client = client || new RvavClient();
    this.catalog = this._catalog();
  },

  run: function () {
    var res = this.client.catalog();
    if (res.status !== 200) {
      gs.error('rvav: the catalog import failed: ' + RvavClient.problem(res));
      return;
    }
    var set = this._variableSet();
    var seen = {};
    var items = res.body.items || [];
    for (var i = 0; i < items.length; i++) {
      seen[this._item(items[i], set)] = true;
    }
    var gone = new GlideRecord('sc_cat_item');
    gone.addQuery('meta', 'STARTSWITH', RvavOrder.ITEM_PREFIX);
    gone.addActiveQuery();
    gone.query();
    while (gone.next()) {
      if (!seen[gone.getUniqueValue()]) {
        gone.setValue('active', false);
        gone.update();
      }
    }
    gs.info('rvav: imported ' + items.length + ' catalog items');
  },

  _catalog: function () {
    var id = gs.getProperty('rvav.catalog', '');
    var gr = new GlideRecord('sc_catalog');
    if (id ? gr.get(id) : gr.get('title', 'Service Catalog')) {
      return gr.getUniqueValue();
    }
    throw new Error('rvav: no catalog ' + (id || 'Service Catalog') + ' (system property rvav.catalog)');
  },

  _item: function (it, set) {
    var gr = new GlideRecord('sc_cat_item');
    var found = gr.get('meta', RvavOrder.ITEM_PREFIX + it.id);
    gr.setValue('name', it.name);
    gr.setValue('short_description', it.description || it.name);
    gr.setValue('meta', RvavOrder.ITEM_PREFIX + it.id);
    gr.setValue('sc_catalogs', this.catalog);
    gr.setValue('category', this._category(it.category || 'rvav'));
    gr.setValue('no_quantity', true);
    gr.setValue('active', true);
    var id = RvavCatalogImport.save(gr, found);

    var link = new GlideRecord('io_set_item');
    link.addQuery('sc_cat_item', id);
    link.addQuery('variable_set', set);
    link.query();
    if (!link.next()) {
      link.initialize();
      link.setValue('sc_cat_item', id);
      link.setValue('variable_set', set);
      link.setValue('order', 100);
      link.insert();
    }

    var kept = {};
    var inputs = it.inputs || [];
    for (var i = 0; i < inputs.length; i++) {
      kept[this._input(id, inputs[i], 1000 + 10 * i)] = true;
    }
    var old = new GlideRecord('item_option_new');
    old.addQuery('cat_item', id);
    old.addQuery('name', 'STARTSWITH', RvavOrder.INPUT_PREFIX);
    old.addActiveQuery();
    old.query();
    while (old.next()) {
      if (!kept[old.getUniqueValue()]) {
        old.setValue('active', false);
        old.update();
      }
    }
    return id;
  },

  _category: function (name) {
    var title = name.charAt(0).toUpperCase() + name.substring(1);
    var gr = new GlideRecord('sc_category');
    gr.addQuery('sc_catalog', this.catalog);
    gr.addQuery('title', title);
    gr.query();
    if (gr.next()) {
      return gr.getUniqueValue();
    }
    gr.initialize();
    gr.setValue('sc_catalog', this.catalog);
    gr.setValue('title', title);
    gr.setValue('active', true);
    return gr.insert();
  },

  _input: function (itemId, input, order) {
    var name = RvavOrder.INPUT_PREFIX + input.name;
    var blank = new GlideRecord('item_option_new');
    blank.initialize();
    if (name.length > blank.getElement('name').getED().getLength()) {
      gs.warn('rvav: the input ' + input.name + ' is too long a name for a variable; it takes its initial value');
      return '';
    }
    var v = new GlideRecord('item_option_new');
    v.addQuery('cat_item', itemId);
    v.addQuery('name', name);
    v.query();
    var found = v.next();
    var type = RvavCatalogImport.SINGLE_LINE;
    if (input.options && input.options.length) {
      type = RvavCatalogImport.SELECT_BOX;
    } else if (input.type === 'boolean') {
      type = RvavCatalogImport.CHECKBOX;
    }
    var hasDefault = input['default'] !== undefined && input['default'] !== null;
    v.setValue('cat_item', itemId);
    v.setValue('name', name);
    v.setValue('question_text', input.label);
    v.setValue('type', type);
    // A required input with an initial value can be left empty: rvav takes
    // the initial value.
    v.setValue('mandatory', input.required && !hasDefault && type !== RvavCatalogImport.CHECKBOX);
    v.setValue('default_value', hasDefault ? String(input['default']) : '');
    var help = input.help || '';
    if (input.dynamic) {
      help = (help ? help + ' ' : '') + 'rvav checks it when the request is submitted.';
    }
    v.setValue('help_text', help);
    v.setValue('order', order);
    v.setValue('include_none', !input.required);
    v.setValue('active', true);
    var id = RvavCatalogImport.save(v, found);
    if (type === RvavCatalogImport.SELECT_BOX) {
      this._choices(id, input.options);
    }
    return id;
  },

  _choices: function (question, options) {
    var want = {};
    for (var i = 0; i < options.length; i++) {
      want[options[i].value] = { label: options[i].label, order: 100 * (i + 1) };
    }
    var gr = new GlideRecord('question_choice');
    gr.addQuery('question', question);
    gr.query();
    while (gr.next()) {
      var w = want[gr.getValue('value')];
      if (!w) {
        gr.deleteRecord();
        continue;
      }
      gr.setValue('text', w.label);
      gr.setValue('order', w.order);
      gr.update();
      delete want[gr.getValue('value')];
    }
    for (var value in want) {
      var c = new GlideRecord('question_choice');
      c.initialize();
      c.setValue('question', question);
      c.setValue('value', value);
      c.setValue('text', want[value].label);
      c.setValue('order', want[value].order);
      c.insert();
    }
  },

  // The variable set every item of rvav's has. Its catalog client scripts
  // (catalog-client-scripts/) fill the project and environment choices with
  // the user's entitlements.
  _variableSet: function () {
    var set = new GlideRecord('item_option_new_set');
    if (!set.get('internal_name', RvavCatalogImport.SET)) {
      set.initialize();
      set.setValue('title', 'rvav');
      set.setValue('internal_name', RvavCatalogImport.SET);
      set.setValue('type', 'one_to_one');
      set.setValue('order', 100);
      set.insert();
    }
    var id = set.getUniqueValue();
    var fixed = [
      ['rvav_project', 'Project', RvavCatalogImport.SELECT_BOX, true, 'The rvav projects you may request this item in'],
      ['rvav_environment', 'Environment', RvavCatalogImport.SELECT_BOX, true, ''],
      ['rvav_deployment_name', 'Deployment name', RvavCatalogImport.SINGLE_LINE, false, 'Left empty, rvav names it'],
      ['rvav_justification', 'Justification', RvavCatalogImport.MULTI_LINE, false, ''],
      ['rvav_accept_warnings', 'Submit despite policy warnings', RvavCatalogImport.CHECKBOX, false,
        "rvav refuses a request its policies warn about, unless this is ticked"]
    ];
    for (var i = 0; i < fixed.length; i++) {
      var v = new GlideRecord('item_option_new');
      v.addQuery('variable_set', id);
      v.addQuery('name', fixed[i][0]);
      v.query();
      if (v.next()) {
        continue;
      }
      v.initialize();
      v.setValue('variable_set', id);
      v.setValue('name', fixed[i][0]);
      v.setValue('question_text', fixed[i][1]);
      v.setValue('type', fixed[i][2]);
      v.setValue('mandatory', fixed[i][3]);
      v.setValue('help_text', fixed[i][4]);
      v.setValue('include_none', true);
      v.setValue('order', 100 * (i + 1));
      v.insert();
    }
    return id;
  },

  type: 'RvavCatalogImport'
};
