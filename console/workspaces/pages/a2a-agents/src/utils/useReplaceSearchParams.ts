/**
 * Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
 *
 * WSO2 LLC. licenses this file to you under the Apache License,
 * Version 2.0 (the "License"); you may not use this file except
 * in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing,
 * software distributed under the License is distributed on an
 * "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
 * KIND, either express or implied.  See the License for the
 * specific language governing permissions and limitations
 * under the License.
 */

import { useEffect, useRef } from 'react';
import { useSearchParams } from 'react-router-dom';

export type SearchParamsMutation = (params: URLSearchParams) => void;

/**
 * [current params, updater]; the updater applies the mutation to a copy of the
 * latest params and replaces history.
 *
 * React Router hands a functional setSearchParams the params of the last
 * render, so two writes in one tick would drop the first. The latest written
 * params are kept in a ref instead, resynced from the location after render.
 */
export function useReplaceSearchParams(): [
  URLSearchParams,
  (mutate: SearchParamsMutation) => void,
] {
  const [searchParams, setSearchParams] = useSearchParams();
  const latestParams = useRef(searchParams);

  useEffect(() => {
    latestParams.current = searchParams;
  }, [searchParams]);

  const replaceSearchParams = (mutate: SearchParamsMutation) => {
    const next = new URLSearchParams(latestParams.current);
    mutate(next);
    latestParams.current = next;
    setSearchParams(next, { replace: true });
  };
  return [searchParams, replaceSearchParams];
}
