/*
 * Catalog client script: "rvav: environments", on the variable set rvav.
 * Type onChange; Variable name rvav_project; UI type All; Applies on a
 * Catalog Item view.
 *
 * Asks RvavAjax again rather than keeping its first answer: g_scratchpad
 * and window aren't there in every UI a catalog item is ordered from.
 */
function onChange(control, oldValue, newValue, isLoading) {
  g_form.clearOptions('rvav_environment');
  g_form.addOption('rvav_environment', '', '-- None --');
  if (newValue === '') {
    return;
  }
  var ga = new GlideAjax('RvavAjax');
  ga.addParam('sysparm_name', 'projects');
  ga.addParam('sysparm_item', g_form.getUniqueValue());
  ga.getXMLAnswer(function (answer) {
    var projects = JSON.parse(answer || '{}').projects || [];
    for (var i = 0; i < projects.length; i++) {
      if (projects[i].value !== newValue) {
        continue;
      }
      var envs = projects[i].environments;
      for (var j = 0; j < envs.length; j++) {
        g_form.addOption('rvav_environment', envs[j].value, envs[j].label);
      }
      if (envs.length === 1) {
        g_form.setValue('rvav_environment', envs[0].value);
      }
    }
  });
}
