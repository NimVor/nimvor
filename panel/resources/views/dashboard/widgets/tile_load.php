<?php
/** @var \NimVor\View\View $this */
echo $this->partial('dashboard/tile', ['key' => 'load', 'label' => 'Load average', 'icon' => 'speedometer', 'series' => 'load1']);
